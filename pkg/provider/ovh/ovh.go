// Package ovh es la implementación de provider.Provider para OVHcloud.
//
// Todavía no está disponible (Available false): tenemos la interfaz, la
// terminología, el formato del registry (Managed Private Registry) y las
// instrucciones de conexión, pero Verify no habla con la API de OVH hasta que
// haya un proyecto de Public Cloud para probar.
//
// OVH no ofrece federación de identidad hacia un issuer externo: la conexión
// usa las credenciales de la API de OVHcloud (application key, application
// secret y consumer key) acotadas a un proyecto de Public Cloud. Las dos
// claves secretas las cifra la API. Sin gestor de secretos propio, los
// clusters usan secret_store "kubernetes".
package ovh

import (
	"context"
	"fmt"
	"strings"

	"rampa.dev/pkg/provider"
)

const (
	// SecretStoreName es el valor especial de clusters.secret_store.
	SecretStoreName = "kubernetes"
	// DefaultEndpoint es el endpoint de la API de OVHcloud.
	DefaultEndpoint = "ovh-eu"
	// TokensURL es donde el cliente crea las credenciales de API.
	TokensURL = "https://www.ovh.com/auth/api/createToken"
	// DefaultModuleSource es la base de los módulos OpenTofu de services.
	DefaultModuleSource = "git::https://github.com/CHANGEME/rampa.git"

	// Claves de CloudAccount.Settings y Credentials.
	SettingEndpoint         = "endpoint"
	SettingApplicationKey   = "application_key"
	SettingRegistryURL      = "registry_url"
	CredentialAppSecret     = "application_secret"
	CredentialConsumerKey   = "consumer_key"
	credentialsDocumentedAt = "https://help.ovhcloud.com/csm/en-api-getting-started-ovhcloud-api"
)

// Provider es la implementación de OVHcloud.
type Provider struct {
	moduleSource string
}

var _ provider.Provider = (*Provider)(nil)

// New crea el provider.
func New() *Provider { return &Provider{moduleSource: DefaultModuleSource} }

func init() {
	provider.Register(New())
}

// Kind implementa provider.Provider.
func (p *Provider) Kind() provider.Kind { return provider.OVH }

// DisplayName implementa provider.Provider.
func (p *Provider) DisplayName() string { return "OVHcloud" }

// Available implementa provider.Provider. OVH llega en la ola tres.
func (p *Provider) Available() bool { return false }

// Auth implementa provider.Provider: credenciales de API del cliente, cifradas en la API.
func (p *Provider) Auth() provider.AuthKind { return provider.AuthToken }

// Terminology implementa provider.Provider con los nombres de OVHcloud.
func (p *Provider) Terminology() provider.Terminology {
	return provider.Terminology{
		Account:     provider.LocalizedTerm{ES: "cuenta de OVHcloud", EN: "OVHcloud account"},
		Project:     provider.LocalizedTerm{ES: "proyecto de Public Cloud", EN: "Public Cloud project"},
		Cluster:     provider.LocalizedTerm{ES: "Managed Kubernetes", EN: "Managed Kubernetes"},
		VM:          provider.LocalizedTerm{ES: "instancia", EN: "instance"},
		Registry:    provider.LocalizedTerm{ES: "Managed Private Registry", EN: "Managed Private Registry"},
		SecretStore: provider.LocalizedTerm{ES: "Secrets de Kubernetes", EN: "Kubernetes Secrets"},
		Region:      provider.LocalizedTerm{ES: "región", EN: "region"},
	}
}

// ConnectInstructions describe cómo crear credenciales de API acotadas al
// proyecto de Public Cloud y qué pegar en la web.
func (p *Provider) ConnectInstructions(req provider.ConnectRequest) provider.Instructions {
	appName := "rampa"
	if req.OrgSlug != "" {
		appName = "rampa-" + req.OrgSlug
	}
	description := fmt.Sprintf(
		"OVHcloud no ofrece federación de identidad, así que Rampa opera con credenciales de la API de OVHcloud que vos creás y podés revocar. "+
			"Rampa guarda las dos claves secretas cifradas y nunca las vuelve a mostrar. Pasos: "+
			"1) Entrá a la pantalla de creación de tokens (el botón de abajo) con tu cuenta de OVHcloud; "+
			"2) nombre %s, validez de 90 días (cuando venzan, creá otras y pegalas acá de nuevo: eso rota las anteriores); "+
			"3) derechos mínimos, acotados a tu proyecto: GET /cloud/project/<proyecto>/*, POST y PUT y DELETE sobre /cloud/project/<proyecto>/kube/*, "+
			"/cloud/project/<proyecto>/instance/*, /cloud/project/<proyecto>/network/*, /cloud/project/<proyecto>/sshkey/*, /cloud/project/<proyecto>/containerRegistry/* "+
			"y GET /me; "+
			"4) pegá acá el id del proyecto de Public Cloud (service name), la application key, la application secret y la consumer key. "+
			"Nota: la conexión con OVHcloud todavía no está habilitada; la cuenta queda registrada pero la verificación va a fallar hasta que lo esté.",
		appName)
	command := "curl -fsS https://eu.api.ovh.com/1.0/cloud/project   # con las cabeceras X-Ovh-Application, X-Ovh-Consumer, X-Ovh-Timestamp y X-Ovh-Signature; ver " + credentialsDocumentedAt
	return provider.Instructions{
		Title:       "Conectar un proyecto de Public Cloud de OVHcloud (próximamente)",
		Description: description,
		URL:         TokensURL,
		Command:     command,
		Fields: []provider.Field{
			{Name: "external_ref", Label: "ID del proyecto de Public Cloud", Placeholder: "0123456789abcdef0123456789abcdef", Help: "El service name del proyecto, en la URL del panel.", Required: true},
			{Name: SettingApplicationKey, Label: "Application key", Placeholder: "xxxxxxxxxxxxxxxx", Required: true},
			{Name: CredentialAppSecret, Label: "Application secret", Placeholder: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", Help: "Se guarda cifrada y no se vuelve a mostrar.", Required: true, Secret: true},
			{Name: CredentialConsumerKey, Label: "Consumer key", Placeholder: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", Help: "Se guarda cifrada y no se vuelve a mostrar.", Required: true, Secret: true},
			{Name: SettingEndpoint, Label: "Endpoint de la API", Placeholder: DefaultEndpoint, Help: "ovh-eu, ovh-ca o ovh-us. Default ovh-eu.", Required: false},
			{Name: SettingRegistryURL, Label: "URL del Managed Private Registry", Placeholder: "xxxx.c1.gra9.container-registry.ovh.net", Help: "Opcional. El registry (Harbor) donde Rampa publica las imágenes.", Required: false},
		},
	}
}

// Verify todavía no está implementado: falta firmar requests de la API de
// OVHcloud y llamar a GET /cloud/project/{serviceName}.
func (p *Provider) Verify(_ context.Context, acc provider.CloudAccount) (provider.Verification, error) {
	return provider.Verification{}, fmt.Errorf("provider ovh: verificar la cuenta %q (proyecto %q): %w", acc.Name, acc.ExternalRef, provider.ErrNotImplemented)
}

// Registry devuelve el host del Managed Private Registry de settings.registry_url.
func (p *Provider) Registry(acc provider.CloudAccount) (string, error) {
	u := strings.TrimSpace(acc.Settings[SettingRegistryURL])
	if u == "" {
		return "", fmt.Errorf("provider ovh: la cuenta %q no tiene settings.%s; poné la URL de tu Managed Private Registry", acc.Name, SettingRegistryURL)
	}
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.TrimRight(u, "/"), nil
}

// SecretStoreSpec devuelve el store especial "kubernetes" sin objeto: OVHcloud
// no tiene gestor de secretos, el agente escribe Secrets de Kubernetes.
func (p *Provider) SecretStoreSpec(provider.CloudAccount, provider.ClusterInfo) provider.SecretStoreSpec {
	return provider.SecretStoreSpec{Name: SecretStoreName, Object: map[string]any{}}
}

// WorkloadIdentityAnnotations está vacío: OVHcloud no tiene identidad de workload para pods.
func (p *Provider) WorkloadIdentityAnnotations(provider.CloudAccount, string) map[string]string {
	return map[string]string{}
}

// ServiceModule apunta a terraform/modules/ovh/<kind>. Ninguno existe todavía.
func (p *Provider) ServiceModule(kind string) (provider.ModuleRef, bool) {
	return provider.ModuleRef{Source: fmt.Sprintf("%s//terraform/modules/ovh/%s?ref=main", p.moduleSource, kind)}, false
}
