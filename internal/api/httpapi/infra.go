package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"rampa.dev/internal/api/auth"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/infra"
	"rampa.dev/internal/api/store"
)

// infraRunResponse es el InfraRun de docs/api.md: la fila más log_tail y el usuario.
type infraRunResponse struct {
	*store.InfraRun
	LogTail         []string    `json:"log_tail"`
	RequestedByUser *store.User `json:"requested_by"`
}

// requireInfra responde 501 si el runner no está listo y devuelve false.
func (a *API) requireInfra(w http.ResponseWriter) bool {
	if a.Infra == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "la infraestructura con OpenTofu no está habilitada en esta instalación")
		return false
	}
	if err := a.Infra.Ready(); err != nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", err.Error())
		return false
	}
	return true
}

func (a *API) infraRunResponses(ctx context.Context, runs []store.InfraRun) ([]infraRunResponse, error) {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		if r.RequestedBy != "" {
			ids = append(ids, r.RequestedBy)
		}
	}
	users, err := a.Store.GetUsersByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]infraRunResponse, 0, len(runs))
	for i := range runs {
		run := &runs[i]
		tail, err := a.Store.InfraRunLogTail(ctx, run.OrgID, run.ID, infra.LogTailLines)
		if err != nil {
			return nil, err
		}
		out = append(out, infraRunResponse{InfraRun: run, LogTail: tail, RequestedByUser: users[run.RequestedBy]})
	}
	return out, nil
}

func (a *API) infraRunResponse(ctx context.Context, run *store.InfraRun) (infraRunResponse, error) {
	out, err := a.infraRunResponses(ctx, []store.InfraRun{*run})
	if err != nil {
		return infraRunResponse{}, err
	}
	return out[0], nil
}

// listInfraModules siempre responde, con runner_ready diciendo si el runner
// puede ejecutar y runner_error con lo que falta.
func (a *API) listInfraModules(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"items": infra.LocalizedModules(auth.Locale(r.Context())), "runner_ready": false}
	switch {
	case a.Infra == nil:
		resp["runner_error"] = loc(r, "la infraestructura con OpenTofu no está habilitada en esta instalación", "OpenTofu infrastructure is not enabled on this server")
	case a.Infra.Ready() != nil:
		resp["runner_error"] = a.Infra.Ready().Error()
	default:
		resp["runner_ready"] = true
	}
	writeJSON(w, http.StatusOK, resp)
}

var clusterNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// domainRe acepta un nombre DNS con al menos dos etiquetas.
var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// createInfraCluster crea un cluster (Kubernetes administrado o máquina con
// k3s) con OpenTofu. module elige el módulo (default: el kind cluster del
// provider de la cuenta) e inputs son sus inputs, validados contra lo que el
// módulo declara. Los campos kubernetes_version, vpc_cidr y admin_principal_arns
// al nivel raíz siguen aceptándose por compatibilidad con el formulario de EKS.
func (a *API) createInfraCluster(w http.ResponseWriter, r *http.Request) {
	if !a.requireInfra(w) {
		return
	}
	// Antes de gastar plata en la nube del cliente: el agente que se va a
	// instalar tiene que poder bajar su imagen y llegar a esta API.
	if err := a.Infra.AgentReachability(auth.Locale(r.Context())); err != nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", err.Error())
		return
	}
	oc := orgCtx(r)
	var body struct {
		CloudAccountID string         `json:"cloud_account_id"`
		Name           string         `json:"name"`
		Env            string         `json:"env"`
		Domain         string         `json:"domain"`
		IngressClass   string         `json:"ingress_class"`
		SecretStore    string         `json:"secret_store"`
		Module         string         `json:"module"`
		Inputs         map[string]any `json:"inputs"`

		KubernetesVersion  string   `json:"kubernetes_version"`
		VPCCIDR            string   `json:"vpc_cidr"`
		AdminPrincipalARNs []string `json:"admin_principal_arns"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !clusterNameRe.MatchString(body.Name) {
		a.fail(w, r, invalid(loc(r, "name inválido: empieza con letra, minúsculas, números y guiones, hasta 40 caracteres", "invalid name: starts with a letter, lowercase letters, digits and hyphens, up to 40 characters")), "")
		return
	}
	if body.Env != store.EnvProd && body.Env != store.EnvNonprod {
		a.fail(w, r, invalid(loc(r, "env debe ser nonprod o prod", "env must be nonprod or prod")), "")
		return
	}
	if err := requireString(r, "cloud_account_id", body.CloudAccountID); err != nil {
		a.fail(w, r, err, "")
		return
	}
	// domain es obligatorio para un cluster administrado. Para una máquina es
	// opcional: si viene vacío, el runner lo fija como <ip>.sslip.io al terminar,
	// que resuelve solo y da URLs sin configurar DNS. Se valida más abajo, cuando
	// ya se sabe el kind del módulo.
	body.Domain = strings.TrimSpace(strings.ToLower(body.Domain))
	if body.Domain != "" && !domainRe.MatchString(body.Domain) {
		a.fail(w, r, invalid(loc(r, "domain inválido: usá un nombre DNS como apps.dev.acme.com", "invalid domain: use a DNS name like apps.dev.acme.com")), "")
		return
	}
	if body.IngressClass == "" {
		body.IngressClass = "nginx"
	}
	// Lo explícito en inputs gana; los campos viejos solo completan lo que falta.
	inputs := map[string]any{}
	for k, v := range body.Inputs {
		inputs[k] = v
	}
	if v := strings.TrimSpace(body.KubernetesVersion); v != "" {
		if _, ok := inputs["kubernetes_version"]; !ok {
			inputs["kubernetes_version"] = v
		}
	}
	if v := strings.TrimSpace(body.VPCCIDR); v != "" {
		if _, ok := inputs["vpc_cidr"]; !ok {
			inputs["vpc_cidr"] = v
		}
	}
	if body.AdminPrincipalARNs != nil {
		if _, ok := inputs["admin_principal_arns"]; !ok {
			inputs["admin_principal_arns"] = body.AdminPrincipalARNs
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	acc, err := a.Store.GetCloudAccount(ctx, oc.Org.ID, body.CloudAccountID)
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	if acc.Status != store.CloudAccountConnected {
		a.fail(w, r, invalid(fmt.Sprintf(loc(r, "la cuenta %s no está conectada (status %s); verificala primero", "account %s is not connected (status %s); verify it first"), acc.Name, acc.Status)), "")
		return
	}
	if acc.Region == "" {
		a.fail(w, r, invalid(loc(r, "la cuenta no tiene región; hacé PATCH con region antes de crear un cluster", "the account has no region; PATCH it with a region before creating a cluster")), "")
		return
	}
	m, err := infra.ResolveModule(acc.Provider, strings.TrimSpace(body.Module))
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	if m.Kind == "cluster" && body.Domain == "" {
		a.fail(w, r, invalid(loc(r, "domain es requerido para un cluster administrado: las apps se publican en <app>-<scope>.<domain>", "domain is required for a managed cluster: apps are published at <app>-<scope>.<domain>")), "")
		return
	}
	typed, err := infra.ValidateInputs(m, inputs)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	if body.SecretStore == "" {
		body.SecretStore = m.SecretStore
	}
	if body.SecretStore == "" {
		body.SecretStore = defaultSecretStore(acc.Provider)
	}
	p := principal(r)
	run, _, err := a.Infra.CreateClusterRun(ctx, infra.ClusterRequest{
		OrgID: oc.Org.ID, RequestedBy: p.User.ID, Account: acc, Module: m.Module, Name: body.Name, Env: body.Env, Domain: body.Domain,
		IngressClass: body.IngressClass, SecretStore: body.SecretStore, Inputs: typed, Actor: p.Actor(),
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			a.fail(w, r, conflict(fmt.Sprintf(loc(r, "ya existe un cluster %q en la organización", "a cluster named %q already exists in the organization"), body.Name)), "")
			return
		}
		a.fail(w, r, err, "run de infraestructura")
		return
	}
	resp, err := a.infraRunResponse(ctx, run)
	if err != nil {
		a.fail(w, r, err, "run de infraestructura")
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (a *API) listInfraRuns(w http.ResponseWriter, r *http.Request) {
	if !a.requireInfra(w) {
		return
	}
	oc := orgCtx(r)
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			a.fail(w, r, invalid(loc(r, "limit debe ser un entero entre 1 y 200", "limit must be an integer between 1 and 200")), "")
			return
		}
		limit = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	runs, err := a.Store.ListInfraRuns(ctx, oc.Org.ID, limit)
	if err != nil {
		a.fail(w, r, err, "runs")
		return
	}
	out, err := a.infraRunResponses(ctx, runs)
	if err != nil {
		a.fail(w, r, err, "runs")
		return
	}
	writeJSON(w, http.StatusOK, items(out))
}

func (a *API) getInfraRun(w http.ResponseWriter, r *http.Request) {
	if !a.requireInfra(w) {
		return
	}
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	run, err := a.Store.GetInfraRun(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "run")
		return
	}
	resp, err := a.infraRunResponse(ctx, run)
	if err != nil {
		a.fail(w, r, err, "run")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) destroyInfraRun(w http.ResponseWriter, r *http.Request) {
	a.requeueInfraRun(w, r, a.Infra.Destroy, "destroy")
}

func (a *API) retryInfraRun(w http.ResponseWriter, r *http.Request) {
	a.requeueInfraRun(w, r, a.Infra.Retry, "retry")
}

func (a *API) requeueInfraRun(w http.ResponseWriter, r *http.Request, op func(context.Context, string, string, store.Actor) (*store.InfraRun, error), what string) {
	if !a.requireInfra(w) {
		return
	}
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	id := chi.URLParam(r, "id")
	run, err := op(ctx, oc.Org.ID, id, principal(r).Actor())
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			current, gerr := a.Store.GetInfraRun(ctx, oc.Org.ID, id)
			status := "desconocido"
			if gerr == nil {
				status = current.Status
			}
			switch what {
			case "destroy":
				a.fail(w, r, conflict(fmt.Sprintf(loc(r, "solo se puede destruir un run en succeeded o failed que no esté corriendo; este está en %s", "only a run in succeeded or failed that is not running can be destroyed; this one is %s"), status)), "")
			default:
				a.fail(w, r, conflict(fmt.Sprintf(loc(r, "solo se puede reintentar un run en failed; este está en %s", "only a failed run can be retried; this one is %s"), status)), "")
			}
			return
		}
		a.fail(w, r, err, "run")
		return
	}
	resp, err := a.infraRunResponse(ctx, run)
	if err != nil {
		a.fail(w, r, err, "run")
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// infraRunLog devuelve el log completo en text/plain o, con ?stream=1, lo
// sigue por SSE: las líneas guardadas, las nuevas en vivo (event: line), los
// cambios de estado (event: status) y event: done al terminar.
func (a *API) infraRunLog(w http.ResponseWriter, r *http.Request) {
	if !a.requireInfra(w) {
		return
	}
	oc := orgCtx(r)
	id := chi.URLParam(r, "id")
	if r.URL.Query().Get("stream") != "1" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := a.Store.GetInfraRun(ctx, oc.Org.ID, id); err != nil {
			a.fail(w, r, err, "run")
			return
		}
		lines, err := a.Store.ListInfraRunLog(ctx, oc.Org.ID, id, 0)
		if err != nil {
			a.fail(w, r, err, "log")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		for _, l := range lines {
			_, _ = fmt.Fprintln(w, l.Line)
		}
		return
	}

	// Suscribirse antes de leer lo guardado: lo que llegue en el medio se
	// deduplica por seq.
	sub := a.Bus.Subscribe(oc.Org.ID, store.EventFilter{SubjectType: infra.SubjectType, SubjectID: id})
	defer a.Bus.Unsubscribe(sub)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	lines, err := a.Store.ListInfraRunLog(ctx, oc.Org.ID, id, 0)
	if err != nil {
		cancel()
		a.fail(w, r, err, "log")
		return
	}
	run, err := a.Store.GetInfraRun(ctx, oc.Org.ID, id)
	cancel()
	if err != nil {
		a.fail(w, r, err, "run")
		return
	}

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(event, data string) bool {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	lastSeq := 0
	for _, l := range lines {
		if !send("line", l.Line) {
			return
		}
		lastSeq = l.Seq
	}
	if !send("status", run.Status) {
		return
	}
	if run.Terminal() {
		send("done", run.Status)
		return
	}

	ticker := time.NewTicker(SSEPingInterval)
	defer ticker.Stop()
	p := principal(r)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			ok := a.Auth.StillAuthorized(checkCtx, p, oc.Org.ID)
			cancel()
			if !ok {
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case e := <-sub.C:
			switch e.Kind {
			case "infra_run.line":
				var payload struct {
					Seq  int    `json:"seq"`
					Line string `json:"line"`
				}
				if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.Seq <= lastSeq {
					continue
				}
				lastSeq = payload.Seq
				if !send("line", payload.Line) {
					return
				}
			case "infra_run.status":
				var payload struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal(e.Payload, &payload); err != nil {
					continue
				}
				if !send("status", payload.Status) {
					return
				}
				if (&store.InfraRun{Status: payload.Status}).Terminal() {
					// Las últimas líneas pueden haber llegado antes que el estado.
					flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					rest, err := a.Store.ListInfraRunLog(flushCtx, oc.Org.ID, id, lastSeq)
					cancel()
					if err == nil {
						for _, l := range rest {
							if !send("line", l.Line) {
								return
							}
							lastSeq = l.Seq
						}
					}
					send("done", payload.Status)
					return
				}
			}
		}
	}
}
