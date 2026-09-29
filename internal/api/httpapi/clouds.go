package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/provider"
)

// Los tres providers siempre se listan, aunque no estén registrados.
var providerKinds = []struct {
	kind provider.Kind
	name string
}{
	{provider.AWS, "Amazon Web Services"},
	{provider.GCP, "Google Cloud"},
	{provider.Azure, "Microsoft Azure"},
}

type providerResponse struct {
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Available   bool   `json:"available"`
}

func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	out := make([]providerResponse, 0, len(providerKinds))
	for _, pk := range providerKinds {
		resp := providerResponse{Kind: string(pk.kind), DisplayName: pk.name}
		if p, ok := provider.Get(pk.kind); ok {
			resp.DisplayName = p.DisplayName()
			resp.Available = p.Available()
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, items(out))
}

// providerFor devuelve el provider registrado y disponible para una cuenta.
func providerFor(kind string) (provider.Provider, error) {
	p, ok := provider.Get(provider.Kind(kind))
	if !ok {
		return nil, invalid(fmt.Sprintf("el provider %q no está disponible en esta instalación", kind))
	}
	if !p.Available() {
		return nil, invalid(fmt.Sprintf("el provider %s todavía no está implementado", p.DisplayName()))
	}
	return p, nil
}

// toProviderAccount convierte la fila al tipo de pkg/provider.
func toProviderAccount(acc *store.CloudAccount) provider.CloudAccount {
	settings := map[string]string{}
	for k, v := range acc.Settings {
		settings[k] = v
	}
	return provider.CloudAccount{
		ID: acc.ID, OrgID: acc.OrgID, Kind: provider.Kind(acc.Provider), Name: acc.Name,
		ExternalRef: acc.ExternalRef, Region: acc.Region, Settings: settings,
	}
}

type cloudAccountResponse struct {
	*store.CloudAccount
	// Instructions es null si el provider no está registrado en esta instalación.
	Instructions *provider.Instructions `json:"instructions"`
}

func (a *API) cloudAccountResponse(orgSlug string, acc *store.CloudAccount) cloudAccountResponse {
	resp := cloudAccountResponse{CloudAccount: acc}
	if p, ok := provider.Get(provider.Kind(acc.Provider)); ok {
		ins := p.ConnectInstructions(provider.ConnectRequest{OrgSlug: orgSlug, ExternalID: acc.Settings["external_id"], CallbackURL: a.Config.PublicURL, Region: acc.Region})
		resp.Instructions = &ins
	}
	return resp
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

func (a *API) createCloudAccount(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	var body struct {
		Provider string            `json:"provider"`
		Name     string            `json:"name"`
		Region   string            `json:"region"`
		Settings map[string]string `json:"settings"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !nameRe.MatchString(body.Name) {
		a.fail(w, r, invalid("name inválido: minúsculas, números y guiones"), "")
		return
	}
	known := false
	for _, pk := range providerKinds {
		if string(pk.kind) == body.Provider {
			known = true
		}
	}
	if !known {
		a.fail(w, r, invalid("provider debe ser aws, gcp o azure"), "")
		return
	}
	if _, err := providerFor(body.Provider); err != nil {
		a.fail(w, r, err, "")
		return
	}
	settings := map[string]string{}
	for k, v := range body.Settings {
		settings[k] = v
	}
	// El external ID lo genera la API: es la defensa contra el confused deputy.
	settings["external_id"] = uuid.NewString()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	acc, err := a.Store.CreateCloudAccount(ctx, &store.CloudAccount{
		OrgID: oc.Org.ID, Provider: body.Provider, Name: body.Name, Region: strings.TrimSpace(body.Region), Settings: settings,
	})
	if err != nil {
		a.fail(w, r, err, "cuenta de nube "+body.Name)
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cloud_account.created", "cloud_account", acc.ID, "cuenta "+acc.Name+" ("+acc.Provider+") creada, pendiente de conexión", map[string]string{"provider": acc.Provider, "name": acc.Name, "region": acc.Region})
	writeJSON(w, http.StatusCreated, a.cloudAccountResponse(oc.Org.Slug, acc))
}

func (a *API) listCloudAccounts(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	accs, err := a.Store.ListCloudAccounts(ctx, oc.Org.ID)
	if err != nil {
		a.fail(w, r, err, "cuentas de nube")
		return
	}
	out := make([]cloudAccountResponse, 0, len(accs))
	for i := range accs {
		out = append(out, a.cloudAccountResponse(oc.Org.Slug, &accs[i]))
	}
	writeJSON(w, http.StatusOK, items(out))
}

func (a *API) getCloudAccount(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	acc, err := a.Store.GetCloudAccount(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	writeJSON(w, http.StatusOK, a.cloudAccountResponse(oc.Org.Slug, acc))
}

func (a *API) updateCloudAccount(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	var body struct {
		ExternalRef *string           `json:"external_ref"`
		Region      *string           `json:"region"`
		Settings    map[string]string `json:"settings"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	acc, err := a.Store.GetCloudAccount(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	externalRef := acc.ExternalRef
	if body.ExternalRef != nil {
		externalRef = strings.TrimSpace(*body.ExternalRef)
	}
	settings := map[string]string{}
	for k, v := range acc.Settings {
		settings[k] = v
	}
	for k, v := range body.Settings {
		if k == "external_id" {
			// Lo genera la API; el cliente no lo cambia.
			continue
		}
		settings[k] = v
	}
	if body.Region != nil && strings.TrimSpace(*body.Region) != "" {
		// region tiene columna propia, pero el cliente puede mandarla acá también.
		acc.Region = strings.TrimSpace(*body.Region)
		settings["region"] = acc.Region
	}
	updated, err := a.Store.UpdateCloudAccountRef(ctx, oc.Org.ID, acc.ID, externalRef, settings)
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cloud_account.updated", "cloud_account", updated.ID, "cuenta "+updated.Name+" actualizada", map[string]string{"external_ref": updated.ExternalRef})
	writeJSON(w, http.StatusOK, a.cloudAccountResponse(oc.Org.Slug, updated))
}

func (a *API) verifyCloudAccount(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	acc, err := a.Store.GetCloudAccount(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	p, err := providerFor(acc.Provider)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	if acc.ExternalRef == "" {
		a.fail(w, r, invalid("la cuenta todavía no tiene external_ref; completá las instrucciones y hacé PATCH primero"), "")
		return
	}
	v, verr := p.Verify(ctx, toProviderAccount(acc))
	status := store.CloudAccountConnected
	if verr != nil {
		v = provider.Verification{OK: false, Message: verr.Error()}
	}
	if !v.OK {
		status = store.CloudAccountFailed
	}
	updated, err := a.Store.SetCloudAccountVerification(ctx, oc.Org.ID, acc.ID, status, store.Verification{OK: v.OK, Identity: v.Identity, Message: v.Message})
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	kind := "cloud_account.verified"
	msg := "cuenta " + updated.Name + " conectada"
	if !v.OK {
		kind = "cloud_account.failed"
		msg = "la verificación de " + updated.Name + " falló: " + v.Message
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), kind, "cloud_account", updated.ID, msg, map[string]any{"ok": v.OK, "identity": v.Identity})
	writeJSON(w, http.StatusOK, a.cloudAccountResponse(oc.Org.Slug, updated))
}

func (a *API) deleteCloudAccount(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	id := chi.URLParam(r, "id")
	if err := a.Store.DeleteCloudAccount(ctx, oc.Org.ID, id); err != nil {
		if err == store.ErrConflict {
			a.fail(w, r, conflict("la cuenta tiene clusters; borrá los clusters primero"), "")
			return
		}
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cloud_account.deleted", "cloud_account", id, "cuenta de nube borrada", nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- clusters ----

type clusterResponse struct {
	*store.Cluster
	Token          string `json:"token,omitempty"`
	InstallCommand string `json:"install_command,omitempty"`
}

// installCommand arma el helm install listo para pegar. Las claves api.url,
// cluster.token, image.repository e image.tag son las de charts/rampa-agent/values.yaml.
// image.* van con --set-string: un tag numérico como 20240101 con --set sería
// un entero y values.schema.json lo rechaza porque exige string.
func (a *API) installCommand(token string) string {
	cmd := fmt.Sprintf("helm upgrade --install rampa-agent %s --namespace rampa-system --create-namespace --set api.url=%s --set cluster.token=%s",
		a.Config.ChartRepo, a.Config.PublicURL, token)
	if repo, tag := splitImage(a.Config.AgentImage); repo != "" {
		cmd += " --set-string image.repository=" + repo
		if tag != "" {
			cmd += " --set-string image.tag=" + tag
		}
	}
	return cmd
}

// splitImage separa "repo:tag" en sus partes. Un ":" dentro del host
// (puerto) no cuenta como separador de tag.
func splitImage(image string) (repo, tag string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return "", ""
	}
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i+1:], "/") {
		return image, ""
	}
	return image[:i], image[i+1:]
}

func (a *API) createCluster(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	var body struct {
		Name           string `json:"name"`
		Env            string `json:"env"`
		CloudAccountID string `json:"cloud_account_id"`
		Domain         string `json:"domain"`
		IngressClass   string `json:"ingress_class"`
		SecretStore    string `json:"secret_store"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !nameRe.MatchString(body.Name) {
		a.fail(w, r, invalid("name inválido: minúsculas, números y guiones"), "")
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
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	acc, err := a.Store.GetCloudAccount(ctx, oc.Org.ID, body.CloudAccountID)
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	if body.SecretStore == "" {
		body.SecretStore = defaultSecretStore(acc.Provider)
	}
	plain, hash, err := auth.NewClusterToken()
	if err != nil {
		a.fail(w, r, err, "token")
		return
	}
	cl, err := a.Store.CreateCluster(ctx, &store.Cluster{
		OrgID: oc.Org.ID, CloudAccountID: acc.ID, Name: body.Name, Env: body.Env, TokenHash: hash,
		Domain: body.Domain, IngressClass: body.IngressClass, SecretStore: body.SecretStore,
	})
	if err != nil {
		a.fail(w, r, err, "cluster "+body.Name)
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cluster.created", "cluster", cl.ID, "cluster "+cl.Name+" ("+cl.Env+") creado", map[string]string{"name": cl.Name, "env": cl.Env, "domain": cl.Domain})
	writeJSON(w, http.StatusCreated, clusterResponse{Cluster: cl, Token: plain, InstallCommand: a.installCommand(plain)})
}

func defaultSecretStore(providerKind string) string {
	switch providerKind {
	case "gcp":
		return "gcp-secret-manager"
	case "azure":
		return "azure-key-vault"
	default:
		return "aws-secrets-manager"
	}
}

func (a *API) listClusters(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cls, err := a.Store.ListClusters(ctx, oc.Org.ID, r.URL.Query().Get("env"))
	if err != nil {
		a.fail(w, r, err, "clusters")
		return
	}
	writeJSON(w, http.StatusOK, items(cls))
}

func (a *API) getCluster(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cl, err := a.Store.GetCluster(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "cluster")
		return
	}
	writeJSON(w, http.StatusOK, cl)
}

func (a *API) deleteCluster(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	id := chi.URLParam(r, "id")
	if err := a.Store.DeleteCluster(ctx, oc.Org.ID, id); err != nil {
		if err == store.ErrConflict {
			a.fail(w, r, conflict("el cluster tiene scopes; borrá los scopes primero"), "")
			return
		}
		a.fail(w, r, err, "cluster")
		return
	}
	// El agente conectado ya no tiene cluster: se lo corta. Al reconectar, su
	// token no resuelve a nada y recibe 401.
	a.Hub.Disconnect(id, "el cluster fue borrado")
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cluster.deleted", "cluster", id, "cluster borrado", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) rotateClusterToken(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	plain, hash, err := auth.NewClusterToken()
	if err != nil {
		a.fail(w, r, err, "token")
		return
	}
	cl, err := a.Store.RotateClusterToken(ctx, oc.Org.ID, chi.URLParam(r, "id"), hash)
	if err != nil {
		a.fail(w, r, err, "cluster")
		return
	}
	// La conexión viva se autenticó con el token viejo: se la corta para que
	// el anterior deje de operar ahora y no cuando el agente decida reconectar.
	a.Hub.Disconnect(cl.ID, "el token del cluster fue rotado")
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cluster.token_rotated", "cluster", cl.ID, "token del cluster "+cl.Name+" rotado; el anterior ya no vale", nil)
	writeJSON(w, http.StatusOK, clusterResponse{Cluster: cl, Token: plain, InstallCommand: a.installCommand(plain)})
}
