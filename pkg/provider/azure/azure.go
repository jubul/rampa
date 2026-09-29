// Package azure es la implementación de provider.Provider para Microsoft Azure.
//
// Todavía no está disponible (Available false): tenemos la interfaz, el formato
// del registry (ACR), el ClusterSecretStore hacia Key Vault y las instrucciones
// de conexión, pero Verify no puede canjear un token federado contra Entra ID
// hasta que haya una suscripción para probar. Ver el README del paquete.
package azure

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"rampa.dev/pkg/provider"
)

const (
	// DefaultSecretStoreName es el nombre del ClusterSecretStore que instala el agente.
	DefaultSecretStoreName = "azure-key-vault"
	// DefaultModuleSource es la base de los módulos OpenTofu de services.
	DefaultModuleSource = "git::https://github.com/CHANGEME/rampa.git"

	// Claves de CloudAccount.Settings.
	SettingTenantID       = "tenant_id"
	SettingClientID       = "client_id"
	SettingSubscriptionID = "subscription_id"
	SettingACRName        = "acr_name"
	SettingKeyVaultName   = "key_vault_name"

	agentServiceAccount = "rampa-agent"
	agentNamespace      = "rampa-system"
)

// acrNamePattern: 5 a 50 caracteres alfanuméricos, según las reglas de ACR.
var acrNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]{5,50}$`)

// Provider es la implementación de Azure.
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
func (p *Provider) Kind() provider.Kind { return provider.Azure }

// DisplayName implementa provider.Provider.
func (p *Provider) DisplayName() string { return "Microsoft Azure" }

// Available implementa provider.Provider. Azure llega en la Fase D.
func (p *Provider) Available() bool { return false }

// ConnectInstructions describe la conexión por App registration con credencial
// federada: Entra ID confía en el issuer de Rampa y el plano de control obtiene
// tokens sin client secret.
func (p *Provider) ConnectInstructions(req provider.ConnectRequest) provider.Instructions {
	orgSlug, externalID, callbackURL := req.OrgSlug, req.ExternalID, req.CallbackURL
	issuer := strings.TrimRight(callbackURL, "/")
	appName := "rampa-connect-" + orgSlug
	description := fmt.Sprintf(
		"Rampa opera en tu suscripción de Azure con una App registration y una credencial federada; no usamos client secrets ni certificados. "+
			"En tu tenant: 1) creá una App registration (por ejemplo %s) con su service principal; "+
			"2) agregale una credencial federada con issuer %s, subject %s y audience api://AzureADTokenExchange; "+
			"3) asignale en la suscripción los roles AcrPush, Azure Kubernetes Service Cluster User Role y Key Vault Secrets Officer (este último acotado al Key Vault de Rampa). "+
			"El comando de abajo hace los tres pasos con az. Después pegá acá el id de la suscripción, el tenant id y el client id (application id). "+
			"Nota: la conexión con Azure todavía no está habilitada; la cuenta queda registrada pero la verificación va a fallar hasta que lo esté.",
		appName, issuer, externalID)

	command := strings.Join([]string{
		"SUBSCRIPTION_ID=$(az account show --query id -o tsv)",
		fmt.Sprintf("APP_ID=$(az ad app create --display-name %s --query appId -o tsv)", appName),
		"az ad sp create --id $APP_ID",
		fmt.Sprintf("az ad app federated-credential create --id $APP_ID --parameters '{\"name\":\"rampa\",\"issuer\":\"%s\",\"subject\":\"%s\",\"audiences\":[\"api://AzureADTokenExchange\"]}'", issuer, externalID),
		"az role assignment create --assignee $APP_ID --role AcrPush --scope /subscriptions/$SUBSCRIPTION_ID",
		"az role assignment create --assignee $APP_ID --role 'Azure Kubernetes Service Cluster User Role' --scope /subscriptions/$SUBSCRIPTION_ID",
		"az role assignment create --assignee $APP_ID --role 'Key Vault Secrets Officer' --scope /subscriptions/$SUBSCRIPTION_ID",
		"echo subscription_id=$SUBSCRIPTION_ID tenant_id=$(az account show --query tenantId -o tsv) client_id=$APP_ID",
	}, "\n")

	return provider.Instructions{
		Title:       "Conectar una suscripción de Azure (próximamente)",
		Description: description,
		Command:     command,
		Fields: []provider.Field{
			{
				Name:        "external_ref",
				Label:       "ID de la suscripción",
				Placeholder: "00000000-0000-0000-0000-000000000000",
				Required:    true,
			},
			{
				Name:        SettingTenantID,
				Label:       "Tenant ID",
				Placeholder: "00000000-0000-0000-0000-000000000000",
				Help:        "El directorio de Entra ID donde vive la App registration.",
				Required:    true,
			},
			{
				Name:        SettingClientID,
				Label:       "Client ID (Application ID)",
				Placeholder: "00000000-0000-0000-0000-000000000000",
				Help:        "El application (client) id de la App registration.",
				Required:    true,
			},
			{
				Name:        SettingACRName,
				Label:       "Nombre del Azure Container Registry",
				Placeholder: "acmerampa",
				Help:        "Solo el nombre, sin .azurecr.io. Las imágenes van a <nombre>.azurecr.io/rampa/<app>.",
				Required:    true,
			},
		},
	}
}

// Verify todavía no está implementado: falta el canje de un token OIDC de Rampa
// por un access token de Entra ID (client_assertion) y la lectura de la suscripción.
func (p *Provider) Verify(_ context.Context, acc provider.CloudAccount) (provider.Verification, error) {
	return provider.Verification{}, fmt.Errorf("provider azure: verificar la cuenta %q (suscripción %q): %w", acc.Name, subscriptionID(acc), provider.ErrNotImplemented)
}

// Registry devuelve el host de ACR: <acr_name>.azurecr.io. ACR es global, no regional.
func (p *Provider) Registry(acc provider.CloudAccount) (string, error) {
	name := strings.TrimSpace(acc.Settings[SettingACRName])
	if name == "" {
		return "", fmt.Errorf("provider azure: la cuenta %q no tiene settings.%s", acc.Name, SettingACRName)
	}
	if !acrNamePattern.MatchString(name) {
		return "", fmt.Errorf("provider azure: nombre de ACR inválido %q: 5 a 50 caracteres alfanuméricos, sin .azurecr.io", name)
	}
	return strings.ToLower(name) + ".azurecr.io", nil
}

// SecretStoreSpec arma el ClusterSecretStore hacia Key Vault con Workload Identity del agente.
// El nombre del vault sale de settings.key_vault_name; si falta, se usa rampa-<cluster>.
func (p *Provider) SecretStoreSpec(acc provider.CloudAccount, cluster provider.ClusterInfo) provider.SecretStoreSpec {
	vault := strings.TrimSpace(acc.Settings[SettingKeyVaultName])
	if vault == "" {
		vault = "rampa-" + cluster.Name
	}
	name := DefaultSecretStoreName
	return provider.SecretStoreSpec{
		Name: name,
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "ClusterSecretStore",
			"metadata": map[string]any{
				"name": name,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by": "rampa",
					"rampa.dev/provider":           string(provider.Azure),
				},
			},
			"spec": map[string]any{
				"provider": map[string]any{
					"azurekv": map[string]any{
						"authType": "WorkloadIdentity",
						"vaultUrl": fmt.Sprintf("https://%s.vault.azure.net", vault),
						"tenantId": strings.TrimSpace(acc.Settings[SettingTenantID]),
						"serviceAccountRef": map[string]any{
							"name":      agentServiceAccount,
							"namespace": agentNamespace,
						},
					},
				},
			},
		},
	}
}

// WorkloadIdentityAnnotations devuelve la anotación de Azure Workload Identity.
func (p *Provider) WorkloadIdentityAnnotations(_ provider.CloudAccount, identity string) map[string]string {
	if strings.TrimSpace(identity) == "" {
		return map[string]string{}
	}
	return map[string]string{"azure.workload.identity/client-id": identity}
}

// ServiceModule apunta a terraform/modules/azure/<kind>. Ninguno existe todavía.
func (p *Provider) ServiceModule(kind string) (provider.ModuleRef, bool) {
	return provider.ModuleRef{Source: fmt.Sprintf("%s//terraform/modules/azure/%s?ref=main", p.moduleSource, kind)}, false
}

// subscriptionID lee la suscripción de ExternalRef, con fallback a settings.subscription_id.
func subscriptionID(acc provider.CloudAccount) string {
	if v := strings.TrimSpace(acc.ExternalRef); v != "" {
		return v
	}
	return strings.TrimSpace(acc.Settings[SettingSubscriptionID])
}
