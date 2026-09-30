package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/deploy"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/protocol"
	"rampa.dev/pkg/render"
)

type scopeResponse struct {
	Name              string              `json:"name"`
	Compute           string              `json:"compute"`
	Ephemeral         bool                `json:"ephemeral"`
	Declared          bool                `json:"declared"`
	ClusterID         string              `json:"cluster_id"`
	Namespace         string              `json:"namespace"`
	URL               string              `json:"url"`
	Status            string              `json:"status"`
	CurrentDeployment *deploymentResponse `json:"current_deployment"`
	Size              string              `json:"size"`
	Replicas          int                 `json:"replicas"`
	Strategy          string              `json:"strategy"`
	TTLExpiresAt      *time.Time          `json:"ttl_expires_at"`
}

// envForScope es el env de cluster que le corresponde a un scope.
func envForScope(scope string) string {
	if scope == "prod" {
		return store.EnvProd
	}
	return store.EnvNonprod
}

// loadApp trae la app y su manifest parseado.
func (a *API) loadApp(ctx context.Context, orgID, name string) (*store.Application, *manifest.Application, error) {
	row, err := a.Store.GetApplicationByName(ctx, orgID, name)
	if err != nil {
		return nil, nil, err
	}
	m, err := manifest.Parse([]byte(row.Manifest))
	if err != nil {
		return nil, nil, fmt.Errorf("el manifest guardado de %s no parsea: %w", row.Name, err)
	}
	return row, m, nil
}

// resolveScopeSpec resuelve un nombre de scope contra el manifest y lo
// traduce a errores de la API.
func resolveScopeSpec(m *manifest.Application, scope string) (manifest.Scope, bool, error) {
	spec, ephemeral, err := m.ResolveScope(scope)
	if err != nil {
		return manifest.Scope{}, false, invalid(err.Error())
	}
	return spec, ephemeral, nil
}

// resolveCluster elige el cluster de un scope: el atado, o el único de la org
// con el env correcto.
func (a *API) resolveCluster(ctx context.Context, orgID string, row *store.Scope, scopeName string) (*store.Cluster, error) {
	env := envForScope(scopeName)
	if row != nil && row.ClusterID != "" {
		cl, err := a.Store.GetCluster(ctx, orgID, row.ClusterID)
		if err != nil {
			return nil, err
		}
		if cl.Env != env {
			return nil, invalid(fmt.Sprintf("el scope %s está atado al cluster %s (env %s) pero necesita env %s", scopeName, cl.Name, cl.Env, env))
		}
		return cl, nil
	}
	cls, err := a.Store.ListClusters(ctx, orgID, env)
	if err != nil {
		return nil, err
	}
	switch len(cls) {
	case 0:
		return nil, invalid(fmt.Sprintf("la organización no tiene clusters env=%s; creá uno con POST /clusters", env))
	case 1:
		return &cls[0], nil
	default:
		return nil, invalid(fmt.Sprintf("hay %d clusters env=%s; atá el scope a uno con PUT /apps/{app}/scopes/%s {\"cluster_id\": ...}", len(cls), env, scopeName))
	}
}

// ttlFor calcula el vencimiento de un scope efímero.
func ttlFor(spec manifest.Scope, ephemeral bool, now time.Time) *time.Time {
	if !ephemeral || spec.TTL == "" {
		return nil
	}
	d, err := time.ParseDuration(spec.TTL)
	if err != nil || d <= 0 {
		return nil
	}
	t := now.Add(d).UTC()
	return &t
}

func (a *API) scopeResponse(ctx context.Context, orgID string, m *manifest.Application, name string, row *store.Scope, clusters map[string]*store.Cluster) (*scopeResponse, error) {
	resp := &scopeResponse{Name: name, Compute: manifest.ComputeKubernetes, Namespace: m.Namespace(name), Status: store.ScopeUnbound}
	spec, ephemeral, err := m.ResolveScope(name)
	if err == nil {
		resp.Declared = !ephemeral
		resp.Ephemeral = ephemeral
		if spec.Compute != "" {
			resp.Compute = spec.Compute
		}
		resp.Size = spec.Size
		resp.Replicas = spec.Replicas
		resp.Strategy = spec.Strategy
		if spec.Autoscaling != nil {
			resp.Replicas = spec.Autoscaling.Min
		}
	}
	if row == nil {
		return resp, nil
	}
	resp.ClusterID = row.ClusterID
	resp.Status = row.Status
	resp.Ephemeral = row.Ephemeral
	resp.TTLExpiresAt = row.TTLExpiresAt
	if row.ClusterID != "" {
		cl := clusters[row.ClusterID]
		if cl == nil {
			c, err := a.Store.GetCluster(ctx, orgID, row.ClusterID)
			if err == nil {
				cl = c
				clusters[row.ClusterID] = c
			}
		}
		if cl != nil && err == nil {
			v, _, rerr := render.Build(m, name, render.Options{Image: "latest", Cluster: &render.Cluster{Domain: cl.Domain, IngressClass: cl.IngressClass, SecretStore: cl.SecretStore}})
			if rerr == nil {
				resp.URL = render.URL(v)
			}
		}
	}
	if row.CurrentDeploymentID != "" {
		dep, err := a.Store.GetDeployment(ctx, orgID, row.CurrentDeploymentID)
		if err == nil {
			dr, err := a.deploymentResponse(ctx, dep)
			if err != nil {
				return nil, err
			}
			resp.CurrentDeployment = dr
		}
	}
	return resp, nil
}

func (a *API) listScopes(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	appRow, m, err := a.loadApp(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	rows, err := a.Store.ListScopes(ctx, oc.Org.ID, appRow.ID)
	if err != nil {
		a.fail(w, r, err, "scopes")
		return
	}
	byName := map[string]*store.Scope{}
	for i := range rows {
		byName[rows[i].Name] = &rows[i]
	}
	names := map[string]bool{}
	for _, n := range declaredScopes(m) {
		if n != manifest.PRScopeKey {
			names[n] = true
		}
	}
	for n := range byName {
		names[n] = true
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	clusters := map[string]*store.Cluster{}
	out := make([]*scopeResponse, 0, len(ordered))
	for _, n := range ordered {
		resp, err := a.scopeResponse(ctx, oc.Org.ID, m, n, byName[n], clusters)
		if err != nil {
			a.fail(w, r, err, "scope")
			return
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, items(out))
}

func (a *API) bindScope(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	scopeName := chi.URLParam(r, "scope")
	var body struct {
		ClusterID string `json:"cluster_id"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	if err := auth.CheckRole(r.Context(), auth.RequiredRoleForScope(scopeName)); err != nil {
		a.fail(w, r, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	appRow, m, err := a.loadApp(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	spec, ephemeral, err := resolveScopeSpec(m, scopeName)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	var cl *store.Cluster
	if body.ClusterID != "" {
		cl, err = a.Store.GetCluster(ctx, oc.Org.ID, body.ClusterID)
		if err != nil {
			a.fail(w, r, err, "cluster")
			return
		}
		if cl.Env != envForScope(scopeName) {
			a.fail(w, r, invalid(fmt.Sprintf("el scope %s necesita un cluster env=%s y %s es env=%s", scopeName, envForScope(scopeName), cl.Name, cl.Env)), "")
			return
		}
	} else {
		cl, err = a.resolveCluster(ctx, oc.Org.ID, nil, scopeName)
		if err != nil {
			a.fail(w, r, err, "")
			return
		}
	}
	existing, err := a.Store.GetScope(ctx, oc.Org.ID, appRow.ID, scopeName)
	if err == nil && existing.ClusterID != "" && existing.ClusterID != cl.ID && existing.Status != store.ScopeIdle && existing.Status != store.ScopeUnbound {
		a.fail(w, r, conflict("el scope está desplegado en otro cluster; hacé undeploy antes de moverlo"), "")
		return
	}
	row, err := a.Store.UpsertScope(ctx, oc.Org.ID, appRow.ID, scopeName, cl.ID, ephemeral, ttlFor(spec, ephemeral, time.Now()))
	if err != nil {
		a.fail(w, r, err, "scope")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "scope.bound", "scope", row.ID, fmt.Sprintf("%s/%s atado al cluster %s", appRow.Name, scopeName, cl.Name), map[string]string{"app": appRow.Name, "scope": scopeName, "cluster_id": cl.ID})
	resp, err := a.scopeResponse(ctx, oc.Org.ID, m, scopeName, row, map[string]*store.Cluster{cl.ID: cl})
	if err != nil {
		a.fail(w, r, err, "scope")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) deleteScope(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	scopeName := chi.URLParam(r, "scope")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	appRow, m, err := a.loadApp(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	row, err := a.Store.GetScope(ctx, oc.Org.ID, appRow.ID, scopeName)
	if err != nil {
		a.fail(w, r, err, "scope")
		return
	}
	// Los permanentes requieren admin; los efímeros, developer.
	min := auth.RoleDeveloper
	if !row.Ephemeral {
		min = auth.RoleAdmin
	}
	if err := auth.CheckRole(r.Context(), min); err != nil {
		a.fail(w, r, err, "")
		return
	}
	if row.ClusterID == "" {
		a.fail(w, r, invalid("el scope no está atado a ningún cluster; no hay nada que borrar"), "")
		return
	}
	if row.Status == store.ScopeDeleting {
		a.fail(w, r, conflict("el scope ya se está borrando"), "")
		return
	}
	cmd := deploy.UndeployCommand(appRow.Name, scopeName)
	payload, _ := json.Marshal(cmd)
	err = a.Store.WithTx(ctx, func(tx *store.Store) error {
		if _, err := tx.CreateAgentCommand(ctx, &store.AgentCommand{
			ID: cmd.ID, OrgID: oc.Org.ID, ClusterID: row.ClusterID, Kind: string(protocol.CommandUndeploy), Payload: payload, ScopeID: row.ID,
		}); err != nil {
			return err
		}
		return tx.SetScopeStatus(ctx, oc.Org.ID, row.ID, store.ScopeDeleting, "")
	})
	if err != nil {
		a.fail(w, r, err, "scope")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "scope.delete_requested", "scope", row.ID, fmt.Sprintf("undeploy de %s/%s encolado", appRow.Name, scopeName), map[string]string{"app": appRow.Name, "scope": scopeName, "command_id": cmd.ID})
	a.Hub.Notify(row.ClusterID)
	row.Status = store.ScopeDeleting
	resp, err := a.scopeResponse(ctx, oc.Org.ID, m, scopeName, row, map[string]*store.Cluster{})
	if err != nil {
		a.fail(w, r, err, "scope")
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}
