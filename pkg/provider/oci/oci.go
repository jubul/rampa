// Package oci es la implementación de provider.Provider para Oracle Cloud
// Infrastructure.
//
// Todavía no está disponible (Available false): tenemos la interfaz, la
// terminología, el formato del registry (OCIR), el ClusterSecretStore hacia OCI
// Vault y las instrucciones de conexión, pero Verify no habla con OCI hasta que
// haya una tenancy para probar y el firmado de requests con la clave de API.
//
// OCI no ofrece federación de identidad hacia un issuer externo para este
// caso: la conexión usa una clave de API (par RSA) de un usuario de IAM con
// políticas acotadas. La clave privada es el campo secreto que la API cifra.
package oci

import (
	"context"
	"fmt"
	"strings"

	"rampa.dev/pkg/provider"
)

const (
	// DefaultSecretStoreName es el nombre del ClusterSecretStore hacia OCI Vault.
	DefaultSecretStoreName = "oci-vault"
	// DefaultModuleSource es la base de los módulos OpenTofu de services.
	DefaultModuleSource = "git::https://github.com/CHANGEME/rampa.git"

	// Claves de CloudAccount.Settings.
	SettingUserOCID        = "user_ocid"
	SettingFingerprint     = "fingerprint"
	SettingCompartmentOCID = "compartment_ocid"
	SettingNamespace       = "object_storage_namespace"
	SettingVaultOCID       = "vault_ocid"
	// CredentialPrivateKey es la clave privada de la API key, cifrada por la API.
	CredentialPrivateKey = "private_key"

	agentServiceAccount = "rampa-agent"
	agentNamespace      = "rampa-system"
)

// Provider es la implementación de OCI.
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
func (p *Provider) Kind() provider.Kind { return provider.OCI }

// DisplayName implementa provider.Provider.
func (p *Provider) DisplayName() string { return "Oracle Cloud Infrastructure" }

// Available implementa provider.Provider. OCI llega en la ola dos.
func (p *Provider) Available() bool { return false }

// Auth implementa provider.Provider: clave de API del cliente, cifrada en la API.
func (p *Provider) Auth() provider.AuthKind { return provider.AuthToken }

// Terminology implementa provider.Provider con los nombres de OCI.
func (p *Provider) Terminology() provider.Terminology {
	return provider.Terminology{
		Account:     provider.LocalizedTerm{ES: "tenancy de OCI", EN: "OCI tenancy"},
		Project:     provider.LocalizedTerm{ES: "compartment", EN: "compartment"},
		Cluster:     provider.LocalizedTerm{ES: "cluster OKE", EN: "OKE cluster"},
		VM:          provider.LocalizedTerm{ES: "instancia de Compute", EN: "Compute instance"},
		Registry:    provider.LocalizedTerm{ES: "OCIR", EN: "OCIR"},
		SecretStore: provider.LocalizedTerm{ES: "OCI Vault", EN: "OCI Vault"},
		Region:      provider.LocalizedTerm{ES: "región", EN: "region"},
	}
}

// ConnectInstructions describe la conexión con una clave de API de un usuario
// de IAM acotado por políticas al compartment de Rampa.
func (p *Provider) ConnectInstructions(req provider.ConnectRequest) provider.Instructions {
	userName := "rampa-connect"
	if req.OrgSlug != "" {
		userName = "rampa-connect-" + req.OrgSlug
	}
	description := fmt.Sprintf(
		"OCI no ofrece federación de identidad hacia Rampa, así que la conexión usa una clave de API (par RSA) de un usuario de IAM propio. "+
			"Rampa guarda la clave privada cifrada y nunca la vuelve a mostrar. Pasos: "+
			"1) Creá un compartment (por ejemplo rampa) y un grupo rampa-admins; "+
			"2) creá el usuario %s, agregalo al grupo y dale una política acotada al compartment: "+
			"manage cluster-family, manage instance-family, manage virtual-network-family, manage repos, manage vaults, manage keys, manage secret-family, read objectstorage-namespaces; "+
			"3) en el usuario, API Keys, Add API Key: generá el par, descargá la clave privada y copiá el fingerprint; "+
			"4) pegá acá el OCID de la tenancy, el OCID del usuario, el fingerprint, la clave privada (PEM) y el OCID del compartment. "+
			"El comando de abajo hace los pasos 1 a 3 con la CLI de OCI. "+
			"Nota: la conexión con OCI todavía no está habilitada; la cuenta queda registrada pero la verificación va a fallar hasta que lo esté.",
		userName)
	command := strings.Join([]string{
		"TENANCY_OCID=$(oci iam compartment list --query 'data[0].\"compartment-id\"' --raw-output)",
		"COMPARTMENT_OCID=$(oci iam compartment create --compartment-id $TENANCY_OCID --name rampa --description 'Rampa' --query data.id --raw-output)",
		"GROUP_OCID=$(oci iam group create --name rampa-admins --description 'Rampa' --query data.id --raw-output)",
		fmt.Sprintf("USER_OCID=$(oci iam user create --name %s --description 'Rampa' --query data.id --raw-output)", userName),
		"oci iam group add-user --group-id $GROUP_OCID --user-id $USER_OCID",
		"oci iam policy create --compartment-id $COMPARTMENT_OCID --name rampa-connect --description 'Rampa' --statements '[" +
			"\"Allow group rampa-admins to manage cluster-family in compartment rampa\"," +
			"\"Allow group rampa-admins to manage instance-family in compartment rampa\"," +
			"\"Allow group rampa-admins to manage virtual-network-family in compartment rampa\"," +
			"\"Allow group rampa-admins to manage repos in tenancy\"," +
			"\"Allow group rampa-admins to manage vaults in compartment rampa\"," +
			"\"Allow group rampa-admins to manage keys in compartment rampa\"," +
			"\"Allow group rampa-admins to manage secret-family in compartment rampa\"," +
			"\"Allow group rampa-admins to read objectstorage-namespaces in tenancy\"]'",
		"openssl genrsa -out rampa-oci.pem 2048 && openssl rsa -pubout -in rampa-oci.pem -out rampa-oci_public.pem",
		"oci iam user api-key upload --user-id $USER_OCID --key-file rampa-oci_public.pem --query data.fingerprint --raw-output",
		"echo tenancy=$TENANCY_OCID user=$USER_OCID compartment=$COMPARTMENT_OCID",
	}, "\n")
	return provider.Instructions{
		Title:       "Conectar una tenancy de OCI (próximamente)",
		Description: description,
		Command:     command,
		Fields: []provider.Field{
			{Name: "external_ref", Label: "OCID de la tenancy", Placeholder: "ocid1.tenancy.oc1..aaaa", Required: true},
			{Name: SettingUserOCID, Label: "OCID del usuario", Placeholder: "ocid1.user.oc1..aaaa", Help: "El usuario de IAM cuya clave de API usa Rampa.", Required: true},
			{Name: SettingFingerprint, Label: "Fingerprint de la clave de API", Placeholder: "aa:bb:cc:...", Required: true},
			{Name: CredentialPrivateKey, Label: "Clave privada (PEM)", Placeholder: "-----BEGIN PRIVATE KEY-----", Help: "Se guarda cifrada y no se vuelve a mostrar.", Required: true, Secret: true},
			{Name: SettingCompartmentOCID, Label: "OCID del compartment", Placeholder: "ocid1.compartment.oc1..aaaa", Help: "Donde Rampa crea clusters, instancias y vaults.", Required: true},
			{Name: SettingNamespace, Label: "Namespace de Object Storage", Placeholder: "axaxnpcrorw5", Help: "Es el namespace de OCIR: <región>.ocir.io/<namespace>. Lo muestra oci os ns get.", Required: false},
		},
	}
}

// Verify todavía no está implementado: falta firmar requests con la clave de
// API y llamar a GET /20160918/tenancies/{tenancyId}.
func (p *Provider) Verify(_ context.Context, acc provider.CloudAccount) (provider.Verification, error) {
	return provider.Verification{}, fmt.Errorf("provider oci: verificar la cuenta %q (tenancy %q): %w", acc.Name, acc.ExternalRef, provider.ErrNotImplemented)
}

// Registry devuelve el host de OCIR: <región>.ocir.io/<namespace>.
func (p *Provider) Registry(acc provider.CloudAccount) (string, error) {
	ns := strings.TrimSpace(acc.Settings[SettingNamespace])
	if ns == "" {
		return "", fmt.Errorf("provider oci: la cuenta %q no tiene settings.%s (namespace de Object Storage, es el de OCIR)", acc.Name, SettingNamespace)
	}
	region := strings.TrimSpace(acc.Region)
	if region == "" {
		return "", fmt.Errorf("provider oci: la cuenta %q no tiene región; OCIR es regional", acc.Name)
	}
	return fmt.Sprintf("%s.ocir.io/%s", region, ns), nil
}

// SecretStoreSpec arma el ClusterSecretStore hacia OCI Vault con la identidad
// de workload de OKE. El OCID del vault sale de settings.vault_ocid.
func (p *Provider) SecretStoreSpec(acc provider.CloudAccount, cluster provider.ClusterInfo) provider.SecretStoreSpec {
	region := cluster.Region
	if region == "" {
		region = acc.Region
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
					"rampa.dev/provider":           string(provider.OCI),
				},
			},
			"spec": map[string]any{
				"provider": map[string]any{
					"oracle": map[string]any{
						"vault":         strings.TrimSpace(acc.Settings[SettingVaultOCID]),
						"region":        region,
						"compartment":   strings.TrimSpace(acc.Settings[SettingCompartmentOCID]),
						"principalType": "Workload",
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

// WorkloadIdentityAnnotations está vacío: la identidad de workload de OKE se
// configura con políticas por namespace y ServiceAccount, no con anotaciones.
func (p *Provider) WorkloadIdentityAnnotations(provider.CloudAccount, string) map[string]string {
	return map[string]string{}
}

// ServiceModule apunta a terraform/modules/oci/<kind>. Ninguno existe todavía.
func (p *Provider) ServiceModule(kind string) (provider.ModuleRef, bool) {
	return provider.ModuleRef{Source: fmt.Sprintf("%s//terraform/modules/oci/%s?ref=main", p.moduleSource, kind)}, false
}
