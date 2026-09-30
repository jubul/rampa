package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/deploy"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/protocol"
)

type deploymentResponse struct {
	ID          string          `json:"id"`
	App         string          `json:"app"`
	Scope       string          `json:"scope"`
	ClusterID   string          `json:"cluster_id"`
	Image       string          `json:"image"`
	Status      string          `json:"status"`
	Message     string          `json:"message"`
	RequestedBy *store.User     `json:"requested_by"`
	ApprovedBy  *store.User     `json:"approved_by"`
	Workload    json.RawMessage `json:"workload"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at"`
	FinishedAt  *time.Time      `json:"finished_at"`
}

func (a *API) deploymentResponses(ctx context.Context, deps []store.Deployment) ([]*deploymentResponse, error) {
	ids := make([]string, 0, len(deps)*2)
	for _, d := range deps {
		if d.RequestedBy != "" {
			ids = append(ids, d.RequestedBy)
		}
		if d.ApprovedBy != "" {
			ids = append(ids, d.ApprovedBy)
		}
	}
	users, err := a.Store.GetUsersByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]*deploymentResponse, 0, len(deps))
	for i := range deps {
		d := &deps[i]
		resp := &deploymentResponse{
			ID: d.ID, App: d.AppName, Scope: d.ScopeName, ClusterID: d.ClusterID, Image: d.Image, Status: d.Status, Message: d.Message,
			Workload: d.Workload, CreatedAt: d.CreatedAt, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt,
		}
		if resp.Workload == nil {
			resp.Workload = json.RawMessage("null")
		}
		resp.RequestedBy = users[d.RequestedBy]
		resp.ApprovedBy = users[d.ApprovedBy]
		out = append(out, resp)
	}
	return out, nil
}

func (a *API) deploymentResponse(ctx context.Context, d *store.Deployment) (*deploymentResponse, error) {
	out, err := a.deploymentResponses(ctx, []store.Deployment{*d})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// imageTagRe es lo que acepta una referencia OCI como tag: hasta 128
// caracteres de [A-Za-z0-9_.-] que no empiezan con "." ni "-".
var imageTagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// validateImage rechaza un tag inválido antes de encolar. Solo aplica cuando
// image no trae "/" (es un tag suelto que se combina con el registry): un
// valor como "v1:latest" armaría una referencia inválida que el agente solo
// reportaría tras el timeout de deploy. Las referencias completas las valida
// el render.
func validateImage(image string) error {
	if strings.Contains(image, "/") {
		return nil
	}
	if !imageTagRe.MatchString(image) {
		return invalid("image no es un tag válido: se aceptan letras, números, '_', '.' y '-' (hasta 128 caracteres, sin empezar con '.' ni '-'), o una referencia completa con '/'")
	}
	return nil
}

// enqueueDeploy persiste el agent_command de deploy y deja el scope en deploying.
// Corre dentro de la transacción del llamador. Si el scope está en deleting,
// devuelve conflict: no se encola un deploy detrás de un undeploy.
func (a *API) enqueueDeploy(ctx context.Context, tx *store.Store, dep *store.Deployment, objects []json.RawMessage) (string, error) {
	cmd := a.Pipeline.BuildCommand(deploy.CommandInput{DeploymentID: dep.ID, App: dep.AppName, Scope: dep.ScopeName, Objects: objects})
	payload, err := json.Marshal(cmd)
	if err != nil {
		return "", err
	}
	if _, err := tx.CreateAgentCommand(ctx, &store.AgentCommand{
		ID: cmd.ID, OrgID: dep.OrgID, ClusterID: dep.ClusterID, Kind: string(protocol.CommandDeploy), Payload: payload,
		DeploymentID: dep.ID, ScopeID: dep.ScopeID,
	}); err != nil {
		return "", err
	}
	if err := tx.SetScopeDeployStatus(ctx, dep.OrgID, dep.ScopeID, store.ScopeDeploying, dep.ID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return "", conflict("el scope se está borrando; esperá a que termine el undeploy")
		}
		return "", err
	}
	return cmd.ID, nil
}

func (a *API) createDeployment(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	p := principal(r)
	scopeName := chi.URLParam(r, "scope")
	var body struct {
		Image   string `json:"image"`
		Message string `json:"message"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Image = strings.TrimSpace(body.Image)
	if err := requireString("image", body.Image); err != nil {
		a.fail(w, r, err, "")
		return
	}
	if err := validateImage(body.Image); err != nil {
		a.fail(w, r, err, "")
		return
	}
	if err := auth.CheckRole(r.Context(), auth.RequiredRoleForScope(scopeName)); err != nil {
		a.fail(w, r, err, "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
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
	if spec.Compute == manifest.ComputeServerless {
		a.fail(w, r, notImplemented(fmt.Sprintf("serverless llega en la ola dos: el scope %s declara compute serverless y hoy solo se despliega compute kubernetes", scopeName)), "")
		return
	}
	scopeRow, err := a.Store.GetScope(ctx, oc.Org.ID, appRow.ID, scopeName)
	if err != nil && err != store.ErrNotFound {
		a.fail(w, r, err, "scope")
		return
	}
	if scopeRow != nil && scopeRow.Status == store.ScopeDeleting {
		a.fail(w, r, conflict("el scope se está borrando; esperá a que termine el undeploy"), "")
		return
	}
	cl, err := a.resolveCluster(ctx, oc.Org.ID, scopeRow, scopeName)
	if err != nil {
		a.fail(w, r, err, "cluster")
		return
	}
	acc, err := a.Store.GetCloudAccount(ctx, oc.Org.ID, cl.CloudAccountID)
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	prov, err := providerFor(acc.Provider)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	registry, err := prov.Registry(toProviderAccount(acc))
	if err != nil {
		a.fail(w, r, invalid("no pude resolver el registry de la cuenta "+acc.Name+": "+err.Error()), "")
		return
	}

	// Render completo antes de persistir nada: si el chart falla, el
	// deployment no existe.
	values, warns, err := a.Pipeline.Values(m, scopeName, body.Image, deploy.Target{Registry: registry, Domain: cl.Domain, IngressClass: cl.IngressClass, SecretStore: cl.SecretStore})
	if err != nil {
		a.fail(w, r, invalid("no pude armar los values: "+err.Error()), "")
		return
	}
	rendered, err := a.Pipeline.RenderObjects(values)
	if err != nil {
		a.fail(w, r, invalid("el chart no renderiza para este scope: "+err.Error()), "")
		return
	}
	valuesMap, err := deploy.ValuesToMap(values)
	if err != nil {
		a.fail(w, r, err, "values")
		return
	}
	valuesJSON, err := json.Marshal(valuesMap)
	if err != nil {
		a.fail(w, r, err, "values")
		return
	}

	status := store.DeploymentQueued
	if scopeName == "prod" && oc.Org.Settings.RequireApprovalProd {
		status = store.DeploymentAwaitingApproval
	}
	fullImage := values.Image.Repository + ":" + values.Image.Tag

	var dep *store.Deployment
	var commandID string
	err = a.Store.WithTx(ctx, func(tx *store.Store) error {
		sc, err := tx.UpsertScope(ctx, oc.Org.ID, appRow.ID, scopeName, cl.ID, ephemeral, ttlFor(spec, ephemeral, time.Now()))
		if err != nil {
			return err
		}
		d, err := tx.CreateDeployment(ctx, &store.Deployment{
			OrgID: oc.Org.ID, ApplicationID: appRow.ID, ScopeID: sc.ID, ClusterID: cl.ID,
			Image: fullImage, Values: valuesJSON, Status: status, Message: strings.TrimSpace(body.Message), RequestedBy: p.User.ID,
		})
		if err != nil {
			return err
		}
		if status == store.DeploymentAwaitingApproval {
			ap, err := tx.CreateApproval(ctx, oc.Org.ID, d.ID, true)
			if err != nil {
				return err
			}
			d, err = tx.UpdateDeployment(ctx, oc.Org.ID, d.ID, store.DeploymentUpdate{ApprovalID: ap.ID})
			if err != nil {
				return err
			}
		} else {
			commandID, err = a.enqueueDeploy(ctx, tx, d, rendered.Objects)
			if err != nil {
				return err
			}
		}
		dep = d
		return nil
	})
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}

	actor := p.Actor()
	payload := map[string]any{"app": dep.AppName, "scope": dep.ScopeName, "image": dep.Image, "cluster_id": dep.ClusterID, "warnings": warns}
	a.Bus.Emit(ctx, oc.Org.ID, actor, "deployment.requested", "deployment", dep.ID, fmt.Sprintf("deploy de %s a %s/%s pedido", dep.Image, dep.AppName, dep.ScopeName), payload)
	if status == store.DeploymentQueued {
		a.Bus.Emit(ctx, oc.Org.ID, actor, "deployment.queued", "deployment", dep.ID, fmt.Sprintf("%s/%s encolado para el cluster %s", dep.AppName, dep.ScopeName, cl.Name), map[string]any{"app": dep.AppName, "scope": dep.ScopeName, "command_id": commandID, "objects": len(rendered.Objects), "kinds": rendered.Kinds})
		a.Hub.Notify(cl.ID)
	} else {
		a.Bus.Emit(ctx, oc.Org.ID, actor, "deployment.awaiting_approval", "deployment", dep.ID, fmt.Sprintf("%s/%s espera aprobación de un admin", dep.AppName, dep.ScopeName), map[string]any{"app": dep.AppName, "scope": dep.ScopeName, "image": dep.Image})
	}
	resp, err := a.deploymentResponse(ctx, dep)
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (a *API) approveDeployment(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	p := principal(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	dep, err := a.Store.GetDeployment(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	if dep.Status != store.DeploymentAwaitingApproval {
		a.fail(w, r, conflict(fmt.Sprintf("el deployment está %s; solo se aprueba en awaiting_approval", dep.Status)), "")
		return
	}
	var vals map[string]any
	if err := json.Unmarshal(dep.Values, &vals); err != nil {
		a.fail(w, r, fmt.Errorf("values guardados corruptos: %w", err), "deployment")
		return
	}
	rendered, err := a.Pipeline.RenderObjectsFromMap(vals, dep.AppName, dep.AppName+"-"+dep.ScopeName)
	if err != nil {
		a.fail(w, r, invalid("el chart no renderiza con los values guardados: "+err.Error()), "")
		return
	}
	var commandID string
	err = a.Store.WithTx(ctx, func(tx *store.Store) error {
		// Transición condicional: si otro approve o un cancel llegó antes, el
		// UPDATE no afecta filas y devolvemos conflict con el estado actual.
		d, err := tx.TransitionDeployment(ctx, oc.Org.ID, dep.ID, []string{store.DeploymentAwaitingApproval},
			store.DeploymentUpdate{Status: store.DeploymentQueued, ApprovedBy: p.User.ID})
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				return a.deploymentStateConflict(ctx, tx, oc.Org.ID, dep.ID, "solo se aprueba en awaiting_approval")
			}
			return err
		}
		// El scope pudo re-atarse a otro cluster mientras esperaba aprobación:
		// el deployment quedó con el cluster original y no se encola ahí.
		sc, err := tx.GetScopeByID(ctx, oc.Org.ID, d.ScopeID)
		if err != nil {
			return err
		}
		if sc.ClusterID != d.ClusterID {
			return conflict("el scope se movió a otro cluster mientras el deployment esperaba aprobación; cancelalo y pedí el deploy de nuevo")
		}
		if err := tx.ApproveApproval(ctx, oc.Org.ID, d.ID, p.User.ID); err != nil {
			return err
		}
		commandID, err = a.enqueueDeploy(ctx, tx, d, rendered.Objects)
		if err != nil {
			return err
		}
		dep = d
		return nil
	})
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	actor := p.Actor()
	a.Bus.Emit(ctx, oc.Org.ID, actor, "deployment.approved", "deployment", dep.ID, fmt.Sprintf("%s/%s aprobado", dep.AppName, dep.ScopeName), map[string]any{"app": dep.AppName, "scope": dep.ScopeName, "approved_by": p.User.ID})
	a.Bus.Emit(ctx, oc.Org.ID, actor, "deployment.queued", "deployment", dep.ID, fmt.Sprintf("%s/%s encolado", dep.AppName, dep.ScopeName), map[string]any{"app": dep.AppName, "scope": dep.ScopeName, "command_id": commandID, "objects": len(rendered.Objects)})
	a.Hub.Notify(dep.ClusterID)
	resp, err := a.deploymentResponse(ctx, dep)
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) cancelDeployment(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	dep, err := a.Store.GetDeployment(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	if err := auth.CheckRole(r.Context(), auth.RequiredRoleForScope(dep.ScopeName)); err != nil {
		a.fail(w, r, err, "")
		return
	}
	if dep.Status != store.DeploymentAwaitingApproval && dep.Status != store.DeploymentQueued {
		a.fail(w, r, conflict(fmt.Sprintf("el deployment está %s; solo se cancela en awaiting_approval o queued", dep.Status)), "")
		return
	}
	now := time.Now().UTC()
	msg := "cancelado por " + principal(r).ActorName()
	err = a.Store.WithTx(ctx, func(tx *store.Store) error {
		// Primero el comando: solo se cancela lo que el agente no tomó (queued,
		// o sent sin ack). Si ya hizo ack, el deploy está corriendo en el
		// cluster y cancelarlo acá mentiría; se espera el result.
		if dep.Status == store.DeploymentQueued {
			n, err := tx.FailQueuedCommandsForDeployment(ctx, oc.Org.ID, dep.ID, msg)
			if err != nil {
				return err
			}
			if n == 0 {
				acked, err := tx.CountAckedCommandsForDeployment(ctx, oc.Org.ID, dep.ID)
				if err != nil {
					return err
				}
				if acked > 0 {
					return conflict("el agente ya tomó el comando; el deploy está en curso y no se puede cancelar")
				}
			}
		}
		d, err := tx.TransitionDeployment(ctx, oc.Org.ID, dep.ID, []string{store.DeploymentAwaitingApproval, store.DeploymentQueued},
			store.DeploymentUpdate{Status: store.DeploymentCancelled, Message: &msg, FinishedAt: &now})
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				return a.deploymentStateConflict(ctx, tx, oc.Org.ID, dep.ID, "solo se cancela en awaiting_approval o queued")
			}
			return err
		}
		// El scope apuntaba al deployment cancelado: vuelve al último healthy
		// que sigue corriendo en el cluster, o queda idle sin deployment actual.
		sc, err := tx.GetScopeByID(ctx, oc.Org.ID, d.ScopeID)
		if err == nil && sc.CurrentDeploymentID == d.ID && sc.Status == store.ScopeDeploying {
			prev, err := tx.LastHealthyDeploymentForScope(ctx, oc.Org.ID, sc.ID, d.ID)
			switch {
			case err == nil:
				err = tx.SetScopeDeployStatus(ctx, oc.Org.ID, sc.ID, store.ScopeHealthy, prev.ID)
			case errors.Is(err, store.ErrNotFound):
				err = tx.ClearScopeDeployment(ctx, oc.Org.ID, sc.ID)
			}
			if err != nil && !errors.Is(err, store.ErrConflict) {
				return err
			}
		}
		dep = d
		return nil
	})
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "deployment.cancelled", "deployment", dep.ID, fmt.Sprintf("%s/%s cancelado", dep.AppName, dep.ScopeName), map[string]any{"app": dep.AppName, "scope": dep.ScopeName})
	resp, err := a.deploymentResponse(ctx, dep)
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// deploymentStateConflict arma el 409 de una transición que no aplicó,
// leyendo el estado actual para que el mensaje diga en qué quedó.
func (a *API) deploymentStateConflict(ctx context.Context, tx *store.Store, orgID, id, rule string) error {
	d, err := tx.GetDeployment(ctx, orgID, id)
	if err != nil {
		return err
	}
	return conflict(fmt.Sprintf("el deployment está %s; %s", d.Status, rule))
}

func (a *API) listDeployments(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	appRow, err := a.Store.GetApplicationByName(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			a.fail(w, r, invalid("limit debe ser un entero entre 1 y 200"), "")
			return
		}
		limit = n
	}
	sc, err := a.Store.GetScope(ctx, oc.Org.ID, appRow.ID, chi.URLParam(r, "scope"))
	if err != nil {
		if err == store.ErrNotFound {
			writeJSON(w, http.StatusOK, items([]*deploymentResponse{}))
			return
		}
		a.fail(w, r, err, "scope")
		return
	}
	deps, err := a.Store.ListDeploymentsForScope(ctx, oc.Org.ID, sc.ID, limit)
	if err != nil {
		a.fail(w, r, err, "deployments")
		return
	}
	out, err := a.deploymentResponses(ctx, deps)
	if err != nil {
		a.fail(w, r, err, "deployments")
		return
	}
	writeJSON(w, http.StatusOK, items(out))
}

func (a *API) getDeployment(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	dep, err := a.Store.GetDeployment(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	resp, err := a.deploymentResponse(ctx, dep)
	if err != nil {
		a.fail(w, r, err, "deployment")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
