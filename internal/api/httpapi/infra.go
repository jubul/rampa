package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
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
	resp := map[string]any{"items": infra.Modules(), "runner_ready": false}
	switch {
	case a.Infra == nil:
		resp["runner_error"] = "la infraestructura con OpenTofu no está habilitada en esta instalación"
	case a.Infra.Ready() != nil:
		resp["runner_error"] = a.Infra.Ready().Error()
	default:
		resp["runner_ready"] = true
	}
	writeJSON(w, http.StatusOK, resp)
}

var (
	clusterNameRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	iamPrincipalRe = regexp.MustCompile(`^arn:aws[a-zA-Z-]*:iam::\d{12}:(user|role)/.+$`)
)

func (a *API) createInfraCluster(w http.ResponseWriter, r *http.Request) {
	if !a.requireInfra(w) {
		return
	}
	oc := orgCtx(r)
	var body struct {
		CloudAccountID     string   `json:"cloud_account_id"`
		Name               string   `json:"name"`
		Env                string   `json:"env"`
		KubernetesVersion  string   `json:"kubernetes_version"`
		VPCCIDR            string   `json:"vpc_cidr"`
		Domain             string   `json:"domain"`
		IngressClass       string   `json:"ingress_class"`
		SecretStore        string   `json:"secret_store"`
		AdminPrincipalARNs []string `json:"admin_principal_arns"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !clusterNameRe.MatchString(body.Name) {
		a.fail(w, r, invalid("name inválido: empieza con letra, minúsculas, números y guiones, hasta 40 caracteres"), "")
		return
	}
	if body.Env != store.EnvProd && body.Env != store.EnvNonprod {
		a.fail(w, r, invalid("env debe ser nonprod o prod"), "")
		return
	}
	if err := requireString("cloud_account_id", body.CloudAccountID); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Domain = strings.TrimSpace(strings.ToLower(body.Domain))
	if err := requireString("domain", body.Domain); err != nil {
		a.fail(w, r, err, "")
		return
	}
	if body.IngressClass == "" {
		body.IngressClass = "nginx"
	}
	body.KubernetesVersion = strings.TrimSpace(body.KubernetesVersion)
	if body.KubernetesVersion == "" {
		body.KubernetesVersion = infra.DefaultKubernetesVersion
	}
	validVersion := false
	for _, v := range infra.KubernetesVersions {
		if v == body.KubernetesVersion {
			validVersion = true
		}
	}
	if !validVersion {
		a.fail(w, r, invalid("kubernetes_version inválida. Opciones: "+strings.Join(infra.KubernetesVersions, ", ")), "")
		return
	}
	body.VPCCIDR = strings.TrimSpace(body.VPCCIDR)
	if body.VPCCIDR != "" {
		ip, ipnet, err := net.ParseCIDR(body.VPCCIDR)
		if err != nil || ip.To4() == nil {
			a.fail(w, r, invalid("vpc_cidr debe ser un rango IPv4 como 10.42.0.0/16"), "")
			return
		}
		if ones, _ := ipnet.Mask.Size(); ones < 16 || ones > 24 {
			a.fail(w, r, invalid("vpc_cidr debe tener un prefijo entre /16 y /24"), "")
			return
		}
		body.VPCCIDR = ipnet.String()
	}
	arns := make([]string, 0, len(body.AdminPrincipalARNs))
	for _, arn := range body.AdminPrincipalARNs {
		arn = strings.TrimSpace(arn)
		if arn == "" {
			continue
		}
		if !iamPrincipalRe.MatchString(arn) {
			a.fail(w, r, invalid(fmt.Sprintf("admin_principal_arns: %q no es el ARN de un usuario o rol IAM", arn)), "")
			return
		}
		arns = append(arns, arn)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	acc, err := a.Store.GetCloudAccount(ctx, oc.Org.ID, body.CloudAccountID)
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	if acc.Status != store.CloudAccountConnected {
		a.fail(w, r, invalid(fmt.Sprintf("la cuenta %s no está conectada (status %s); verificala primero", acc.Name, acc.Status)), "")
		return
	}
	if acc.Region == "" {
		a.fail(w, r, invalid("la cuenta no tiene región; hacé PATCH con region antes de crear un cluster"), "")
		return
	}
	if m, ok := infra.FindModule(acc.Provider, store.InfraKindCluster); !ok || !m.Available {
		a.fail(w, r, invalid(fmt.Sprintf("todavía no hay módulo de cluster para el provider %s", acc.Provider)), "")
		return
	}
	if body.SecretStore == "" {
		body.SecretStore = defaultSecretStore(acc.Provider)
	}
	p := principal(r)
	run, _, err := a.Infra.CreateClusterRun(ctx, infra.ClusterRequest{
		OrgID: oc.Org.ID, RequestedBy: p.User.ID, Account: acc, Name: body.Name, Env: body.Env, Domain: body.Domain,
		IngressClass: body.IngressClass, SecretStore: body.SecretStore, KubernetesVersion: body.KubernetesVersion,
		VPCCIDR: body.VPCCIDR, AdminPrincipalARNs: arns, Actor: p.Actor(),
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			a.fail(w, r, conflict(fmt.Sprintf("ya existe un cluster %q en la organización", body.Name)), "")
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
			a.fail(w, r, invalid("limit debe ser un entero entre 1 y 200"), "")
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
				a.fail(w, r, conflict(fmt.Sprintf("solo se puede destruir un run en succeeded o failed que no esté corriendo; este está en %s", status)), "")
			default:
				a.fail(w, r, conflict(fmt.Sprintf("solo se puede reintentar un run en failed; este está en %s", status)), "")
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
