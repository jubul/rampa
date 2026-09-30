package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/provider"
)

// Los seis providers conocidos siempre se listan, aunque no estén registrados
// en esta instalación. El auth por defecto es el del ADR 0004.
var providerKinds = []struct {
	kind provider.Kind
	name string
	auth provider.AuthKind
}{
	{provider.AWS, "Amazon Web Services", provider.AuthFederated},
	{provider.GCP, "Google Cloud", provider.AuthFederated},
	{provider.Azure, "Microsoft Azure", provider.AuthFederated},
	{provider.DigitalOcean, "DigitalOcean", provider.AuthToken},
	{provider.OCI, "Oracle Cloud Infrastructure", provider.AuthToken},
	{provider.OVH, "OVHcloud", provider.AuthToken},
}

func providerKindNames() string {
	names := make([]string, 0, len(providerKinds))
	for _, pk := range providerKinds {
		names = append(names, string(pk.kind))
	}
	return strings.Join(names, ", ")
}

type providerResponse struct {
	Kind        string                `json:"kind"`
	DisplayName string                `json:"display_name"`
	Available   bool                  `json:"available"`
	Auth        string                `json:"auth"`
	Terminology *provider.Terminology `json:"terminology"`
}

func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	out := make([]providerResponse, 0, len(providerKinds))
	for _, pk := range providerKinds {
		resp := providerResponse{Kind: string(pk.kind), DisplayName: pk.name, Auth: string(pk.auth)}
		if p, ok := provider.Get(pk.kind); ok {
			resp.DisplayName = p.DisplayName()
			resp.Available = p.Available()
			resp.Auth = string(p.Auth())
			term := p.Terminology()
			resp.Terminology = &term
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, items(out))
}

// requireCredentialsCipher responde 501 si el provider guarda credenciales
// (auth token) y la instalación no tiene RAMPA_STATE_KEY para cifrarlas.
func (a *API) requireCredentialsCipher(p provider.Provider) error {
	if p.Auth() == provider.AuthToken && a.Cipher == nil {
		return notImplemented(fmt.Sprintf("las cuentas de %s se conectan con un token de API que Rampa guarda cifrado, y esta instalación no tiene RAMPA_STATE_KEY; configurala (openssl rand -base64 32) y reiniciá la API", p.DisplayName()))
	}
	return nil
}

// connectRequest arma el ConnectRequest de una cuenta.
func (a *API) connectRequest(orgSlug string, acc *store.CloudAccount) provider.ConnectRequest {
	return provider.ConnectRequest{OrgSlug: orgSlug, ExternalID: acc.Settings["external_id"], CallbackURL: a.Config.PublicURL, Region: acc.Region}
}

// secretFieldNames devuelve los nombres de los campos marcados secret en las instrucciones.
func secretFieldNames(ins provider.Instructions) map[string]bool {
	out := map[string]bool{}
	for _, f := range ins.Fields {
		if f.Secret {
			out[f.Name] = true
		}
	}
	return out
}

// missingConnectFields lista los campos requeridos por las instrucciones que
// la cuenta todavía no tiene: external_ref, credenciales o settings.
func missingConnectFields(ins provider.Instructions, acc *store.CloudAccount) []string {
	var missing []string
	for _, f := range ins.Fields {
		if !f.Required {
			continue
		}
		switch {
		case f.Name == "external_ref":
			if strings.TrimSpace(acc.ExternalRef) == "" {
				missing = append(missing, f.Name)
			}
		case f.Secret:
			if !acc.HasCredentials {
				missing = append(missing, f.Name)
			}
		default:
			if strings.TrimSpace(acc.Settings[f.Name]) == "" {
				missing = append(missing, f.Name)
			}
		}
	}
	return missing
}

// providerAccount arma la cuenta para el provider y, si tiene credenciales
// cifradas, las descifra en memoria para esta llamada.
func (a *API) providerAccount(ctx context.Context, acc *store.CloudAccount) (provider.CloudAccount, error) {
	pacc := toProviderAccount(acc)
	if !acc.HasCredentials {
		return pacc, nil
	}
	if a.Cipher == nil {
		return pacc, notImplemented("la cuenta tiene credenciales cifradas pero esta instalación no tiene RAMPA_STATE_KEY para descifrarlas")
	}
	ct, err := a.Store.GetCloudAccountCredentials(ctx, acc.OrgID, acc.ID)
	if err != nil {
		return pacc, err
	}
	creds, err := a.Cipher.OpenCredentials(acc.ID, ct)
	if err != nil {
		return pacc, err
	}
	pacc.Credentials = creds
	return pacc, nil
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
		ins := p.ConnectInstructions(a.connectRequest(orgSlug, acc))
		resp.Instructions = &ins
		// Por las dudas: settings nunca lleva un campo secreto.
		for name := range secretFieldNames(ins) {
			delete(acc.Settings, name)
		}
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
		a.fail(w, r, invalid("provider debe ser uno de: "+providerKindNames()), "")
		return
	}
	p, err := providerFor(body.Provider)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	if err := a.requireCredentialsCipher(p); err != nil {
		a.fail(w, r, err, "")
		return
	}
	secretNames := secretFieldNames(p.ConnectInstructions(provider.ConnectRequest{OrgSlug: oc.Org.Slug, CallbackURL: a.Config.PublicURL, Region: body.Region}))
	settings := map[string]string{}
	for k, v := range body.Settings {
		if secretNames[k] {
			// Los secretos van por PATCH con credentials, nunca en settings.
			continue
		}
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
		// Credentials son los campos marcados secret en instructions.fields (por
		// ejemplo api_token). Se cifran y nunca se devuelven. También se aceptan
		// dentro de settings, de donde se sacan antes de guardar.
		Credentials map[string]string `json:"credentials"`
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
	// Sin el provider no se sabe qué campos son secretos: seguir dejaría
	// guardar en claro en settings un secreto mandado ahí.
	prov, ok := provider.Get(provider.Kind(acc.Provider))
	if !ok {
		a.fail(w, r, notImplemented(fmt.Sprintf("el provider %s de esta cuenta no está disponible en esta versión; no se puede actualizar", acc.Provider)), "")
		return
	}
	secretNames := secretFieldNames(prov.ConnectInstructions(a.connectRequest(oc.Org.Slug, acc)))
	externalRef := acc.ExternalRef
	if body.ExternalRef != nil {
		externalRef = strings.TrimSpace(*body.ExternalRef)
	}
	settings := map[string]string{}
	for k, v := range acc.Settings {
		if !secretNames[k] {
			settings[k] = v
		}
	}
	newCreds := map[string]string{}
	for k, v := range body.Settings {
		if k == "external_id" {
			// Lo genera la API; el cliente no lo cambia.
			continue
		}
		if secretNames[k] {
			if strings.TrimSpace(v) != "" {
				newCreds[k] = strings.TrimSpace(v)
			}
			continue
		}
		settings[k] = v
	}
	for k, v := range body.Credentials {
		if !secretNames[k] {
			a.fail(w, r, invalid(fmt.Sprintf("credentials.%s no es un campo secreto de %s", k, acc.Provider)), "")
			return
		}
		if strings.TrimSpace(v) != "" {
			newCreds[k] = strings.TrimSpace(v)
		}
	}
	if body.Region != nil && strings.TrimSpace(*body.Region) != "" {
		// region tiene columna propia, pero el cliente puede mandarla acá también.
		acc.Region = strings.TrimSpace(*body.Region)
		settings["region"] = acc.Region
	}
	// Con credenciales nuevas hace falta el cifrado: se valida antes de tocar
	// la base, para que un 501 no deje settings o external_ref a medio escribir.
	var sealed []byte
	if len(newCreds) > 0 {
		if err := a.requireCredentialsCipher(prov); err != nil {
			a.fail(w, r, err, "")
			return
		}
		if a.Cipher == nil {
			a.fail(w, r, notImplemented("esta instalación no tiene RAMPA_STATE_KEY para cifrar credenciales"), "")
			return
		}
		// Volver a mandar un campo lo rota; los demás campos secretos se conservan.
		merged := map[string]string{}
		if acc.HasCredentials {
			ct, err := a.Store.GetCloudAccountCredentials(ctx, oc.Org.ID, acc.ID)
			if err != nil {
				a.fail(w, r, err, "cuenta de nube")
				return
			}
			if prev, err := a.Cipher.OpenCredentials(acc.ID, ct); err == nil {
				merged = prev
			} else {
				a.Log.Warn("credenciales previas ilegibles, se reemplazan", "account_id", acc.ID, "err", err)
			}
		}
		for k, v := range newCreds {
			merged[k] = v
		}
		ct, err := a.Cipher.SealCredentials(acc.ID, merged)
		if err != nil {
			a.fail(w, r, err, "credenciales")
			return
		}
		sealed = ct
	}
	// Settings, external_ref y credenciales se escriben juntos o no se escribe nada.
	var updated *store.CloudAccount
	rotated := sealed != nil
	err = a.Store.WithTx(ctx, func(tx *store.Store) error {
		u, err := tx.UpdateCloudAccountRef(ctx, oc.Org.ID, acc.ID, externalRef, settings)
		if err != nil {
			return err
		}
		if rotated {
			if u, err = tx.SetCloudAccountCredentials(ctx, oc.Org.ID, acc.ID, sealed); err != nil {
				return err
			}
		}
		updated = u
		return nil
	})
	if err != nil {
		a.fail(w, r, err, "cuenta de nube")
		return
	}
	credKeys := make([]string, 0, len(newCreds))
	for k := range newCreds {
		credKeys = append(credKeys, k)
	}
	sort.Strings(credKeys)
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cloud_account.updated", "cloud_account", updated.ID, "cuenta "+updated.Name+" actualizada",
		map[string]any{"external_ref": updated.ExternalRef, "credentials_rotated": rotated, "credential_fields": credKeys})
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
	if missing := missingConnectFields(p.ConnectInstructions(a.connectRequest(oc.Org.Slug, acc)), acc); len(missing) > 0 {
		a.fail(w, r, invalid(fmt.Sprintf("la cuenta todavía no tiene %s; completá las instrucciones y hacé PATCH primero", strings.Join(missing, ", "))), "")
		return
	}
	pacc, err := a.providerAccount(ctx, acc)
	if err != nil {
		a.fail(w, r, err, "credenciales")
		return
	}
	v, verr := p.Verify(ctx, pacc)
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
		if errors.Is(err, store.ErrConflict) {
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

// installCommand arma el helm install listo para pegar con el token del
// cluster. Las claves api.url, cluster.token, image.repository e image.tag son
// las de charts/rampa-agent/values.yaml. image.* van con --set-string: un tag
// numérico como 20240101 con --set sería un entero y values.schema.json lo
// rechaza porque exige string. Si el cluster tiene agent_role_arn, se anota el
// ServiceAccount con IRSA (los puntos de la clave van escapados para que Helm
// no los tome como separadores).
func (a *API) installCommand(token, agentRoleARN string) string {
	return a.installCommandWith("--set cluster.token="+token, agentRoleARN)
}

// bootstrapInstallCommand es installCommand con un bootstrap token de un solo
// uso (cluster.bootstrapToken): el token real no queda escrito en ningún lado
// y el agente lo canjea al arrancar.
func (a *API) bootstrapInstallCommand(bootstrapToken, agentRoleARN string) string {
	return a.installCommandWith("--set-string cluster.bootstrapToken="+bootstrapToken, agentRoleARN)
}

func (a *API) installCommandWith(tokenFlag, agentRoleARN string) string {
	cmd := fmt.Sprintf("helm upgrade --install rampa-agent %s --namespace rampa-system --create-namespace --set api.url=%s %s",
		a.Config.ChartRepo, a.Config.PublicURL, tokenFlag)
	if repo, tag := splitImage(a.Config.AgentImage); repo != "" {
		cmd += " --set-string image.repository=" + repo
		if tag != "" {
			cmd += " --set-string image.tag=" + tag
		}
	}
	if arn := strings.TrimSpace(agentRoleARN); arn != "" {
		cmd += ` --set-string 'serviceAccount.annotations.eks\.amazonaws\.com/role-arn=` + strings.ReplaceAll(arn, "'", `'''`) + `'`
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
		Kind           string `json:"kind"`
		Env            string `json:"env"`
		CloudAccountID string `json:"cloud_account_id"`
		Domain         string `json:"domain"`
		IngressClass   string `json:"ingress_class"`
		SecretStore    string `json:"secret_store"`
		AgentRoleARN   string `json:"agent_role_arn"`
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
	body.Kind = strings.TrimSpace(body.Kind)
	if body.Kind == "" {
		body.Kind = store.ClusterKindKubernetes
	}
	if body.Kind != store.ClusterKindKubernetes && body.Kind != store.ClusterKindVM {
		a.fail(w, r, invalid("kind debe ser kubernetes o vm"), "")
		return
	}
	body.AgentRoleARN = strings.TrimSpace(body.AgentRoleARN)
	if body.AgentRoleARN != "" && !strings.HasPrefix(body.AgentRoleARN, "arn:") {
		a.fail(w, r, invalid("agent_role_arn debe ser el ARN del rol IAM del agente (arn:aws:iam::...:role/...)"), "")
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
		OrgID: oc.Org.ID, CloudAccountID: acc.ID, Name: body.Name, Kind: body.Kind, Env: body.Env, TokenHash: hash,
		Domain: body.Domain, IngressClass: body.IngressClass, SecretStore: body.SecretStore, AgentRoleARN: body.AgentRoleARN,
	})
	if err != nil {
		a.fail(w, r, err, "cluster "+body.Name)
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cluster.created", "cluster", cl.ID, "cluster "+cl.Name+" ("+cl.Env+", "+cl.Kind+") creado", map[string]string{"name": cl.Name, "kind": cl.Kind, "env": cl.Env, "domain": cl.Domain})
	writeJSON(w, http.StatusCreated, clusterResponse{Cluster: cl, Token: plain, InstallCommand: a.installCommand(plain, cl.AgentRoleARN)})
}

// defaultSecretStore es el clusters.secret_store por defecto de cada nube:
// el ClusterSecretStore de su gestor de secretos, o kubernetes donde no hay
// gestor (DigitalOcean, OVH).
func defaultSecretStore(providerKind string) string {
	switch providerKind {
	case "gcp":
		return "gcp-secret-manager"
	case "azure":
		return "azure-key-vault"
	case "oci":
		return "oci-vault"
	case "digitalocean", "ovh":
		return store.SecretStoreKubernetes
	default:
		return "aws-secrets-manager"
	}
}

// BootstrapTokenTTL es cuánto vale un bootstrap token emitido por la API.
const BootstrapTokenTTL = 2 * time.Hour

// issueBootstrapToken genera y guarda un bootstrap token del cluster.
func (a *API) issueBootstrapToken(ctx context.Context, cl *store.Cluster, createdBy string) (plain string, bt *store.BootstrapToken, err error) {
	plain, hash, err := auth.NewBootstrapToken()
	if err != nil {
		return "", nil, err
	}
	bt, err = a.Store.CreateBootstrapToken(ctx, cl.OrgID, cl.ID, hash, time.Now().Add(BootstrapTokenTTL), createdBy)
	if err != nil {
		return "", nil, err
	}
	return plain, bt, nil
}

// createBootstrapToken emite un bootstrap token de un solo uso para instalar
// el agente donde el token real no debe quedar escrito (cloud-init, scripts).
func (a *API) createBootstrapToken(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cl, err := a.Store.GetCluster(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "cluster")
		return
	}
	plain, bt, err := a.issueBootstrapToken(ctx, cl, principal(r).User.ID)
	if err != nil {
		a.fail(w, r, err, "bootstrap token")
		return
	}
	a.Log.Info("bootstrap token emitido", "cluster_id", cl.ID, "org_id", cl.OrgID, "by", principal(r).ActorID(), "expires_at", bt.ExpiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{"bootstrap_token": plain, "expires_at": bt.ExpiresAt, "cluster_id": cl.ID})
}

// clusterInstallCommand regenera el install_command con un bootstrap token
// nuevo, sin rotar el token del cluster: el agente que ya corre no se entera.
func (a *API) clusterInstallCommand(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cl, err := a.Store.GetCluster(ctx, oc.Org.ID, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err, "cluster")
		return
	}
	plain, bt, err := a.issueBootstrapToken(ctx, cl, principal(r).User.ID)
	if err != nil {
		a.fail(w, r, err, "bootstrap token")
		return
	}
	a.Log.Info("install command con bootstrap token emitido", "cluster_id", cl.ID, "org_id", cl.OrgID, "by", principal(r).ActorID())
	writeJSON(w, http.StatusOK, map[string]any{"install_command": a.bootstrapInstallCommand(plain, cl.AgentRoleARN), "expires_at": bt.ExpiresAt})
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
		if errors.Is(err, store.ErrConflict) {
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
	// Rotar e invalidar los bootstrap tokens pendientes van juntos: si no, un
	// bootstrap emitido antes de la rotación seguiría canjeable.
	var cl *store.Cluster
	err = a.Store.WithTx(ctx, func(tx *store.Store) error {
		c, err := tx.RotateClusterToken(ctx, oc.Org.ID, chi.URLParam(r, "id"), hash)
		if err != nil {
			return err
		}
		if _, err := tx.InvalidatePendingBootstrapTokens(ctx, c.OrgID, c.ID); err != nil {
			return err
		}
		cl = c
		return nil
	})
	if err != nil {
		a.fail(w, r, err, "cluster")
		return
	}
	// La conexión viva se autenticó con el token viejo: se la corta para que
	// el anterior deje de operar ahora y no cuando el agente decida reconectar.
	a.Hub.Disconnect(cl.ID, "el token del cluster fue rotado")
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "cluster.token_rotated", "cluster", cl.ID, "token del cluster "+cl.Name+" rotado; el anterior ya no vale", nil)
	writeJSON(w, http.StatusOK, clusterResponse{Cluster: cl, Token: plain, InstallCommand: a.installCommand(plain, cl.AgentRoleARN)})
}
