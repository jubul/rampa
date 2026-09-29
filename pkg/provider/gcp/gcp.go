// Package gcp es la implementación de provider.Provider para Google Cloud.
//
// Todavía no está disponible (Available false): tenemos la interfaz, el formato
// del registry, el ClusterSecretStore y las instrucciones de conexión, pero
// Verify no puede intercambiar tokens con Workload Identity Federation hasta
// que haya una cuenta de GCP para probar. Ver el README del paquete.
package gcp

import (
	"context"
	"fmt"
	"strings"

	"rampa.dev/pkg/provider"
)

const (
	// DefaultSecretStoreName es el nombre del ClusterSecretStore que instala el agente.
	DefaultSecretStoreName = "gcp-secret-manager"
	// DefaultRegion se usa cuando la cuenta no trae región.
	DefaultRegion = "us-central1"
	// DefaultModuleSource es la base de los módulos OpenTofu de services.
	DefaultModuleSource = "git::https://github.com/CHANGEME/rampa.git"

	// Claves de CloudAccount.Settings.
	SettingProjectID                = "project_id"
	SettingServiceAccountEmail      = "service_account_email"
	SettingWorkloadIdentityProvider = "workload_identity_provider"

	agentServiceAccount = "rampa-agent"
	agentNamespace      = "rampa-system"
)

// Provider es la implementación de GCP.
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
func (p *Provider) Kind() provider.Kind { return provider.GCP }

// DisplayName implementa provider.Provider.
func (p *Provider) DisplayName() string { return "Google Cloud" }

// Available implementa provider.Provider. GCP llega en la Fase C.
func (p *Provider) Available() bool { return false }

// ConnectInstructions describe la conexión por Workload Identity Federation:
// un pool con un provider OIDC que confía en el issuer de Rampa, y una service
// account que el plano de control impersona.
func (p *Provider) ConnectInstructions(req provider.ConnectRequest) provider.Instructions {
	orgSlug, externalID, callbackURL := req.OrgSlug, req.ExternalID, req.CallbackURL
	issuer := strings.TrimRight(callbackURL, "/")
	poolID := "rampa-" + orgSlug
	description := fmt.Sprintf(
		"Rampa opera en tu proyecto de GCP con Workload Identity Federation; no usamos claves de service account. "+
			"En tu proyecto: 1) creá un Workload Identity Pool (por ejemplo %s) con un provider OIDC cuyo issuer sea %s y que acepte el audience %s; "+
			"2) creá una service account (por ejemplo rampa-connect@<proyecto>.iam.gserviceaccount.com) con los roles roles/artifactregistry.writer, "+
			"roles/container.clusterViewer y roles/secretmanager.admin acotado a secretos con prefijo rampa-; "+
			"3) dale roles/iam.workloadIdentityUser a principal://iam.googleapis.com/projects/<número>/locations/global/workloadIdentityPools/%s/subject/%s. "+
			"El comando de abajo hace los tres pasos. Después pegá acá el id del proyecto, el email de la service account y el nombre completo del provider. "+
			"Nota: la conexión con GCP todavía no está habilitada; la cuenta queda registrada pero la verificación va a fallar hasta que lo esté.",
		poolID, issuer, externalID, poolID, externalID)

	command := strings.Join([]string{
		"PROJECT_ID=<tu-proyecto>",
		fmt.Sprintf("gcloud iam workload-identity-pools create %s --project=$PROJECT_ID --location=global --display-name='Rampa'", poolID),
		fmt.Sprintf("gcloud iam workload-identity-pools providers create-oidc rampa --project=$PROJECT_ID --location=global --workload-identity-pool=%s \\\n  --issuer-uri=%s --allowed-audiences=%s --attribute-mapping='google.subject=assertion.sub'", poolID, issuer, externalID),
		"gcloud iam service-accounts create rampa-connect --project=$PROJECT_ID --display-name='Rampa'",
		"gcloud projects add-iam-policy-binding $PROJECT_ID --member=serviceAccount:rampa-connect@$PROJECT_ID.iam.gserviceaccount.com --role=roles/artifactregistry.writer",
		"gcloud projects add-iam-policy-binding $PROJECT_ID --member=serviceAccount:rampa-connect@$PROJECT_ID.iam.gserviceaccount.com --role=roles/container.clusterViewer",
		"gcloud projects add-iam-policy-binding $PROJECT_ID --member=serviceAccount:rampa-connect@$PROJECT_ID.iam.gserviceaccount.com --role=roles/secretmanager.admin",
		fmt.Sprintf("PROJECT_NUMBER=$(gcloud projects describe $PROJECT_ID --format='value(projectNumber)')\n"+
			"gcloud iam service-accounts add-iam-policy-binding rampa-connect@$PROJECT_ID.iam.gserviceaccount.com --project=$PROJECT_ID \\\n"+
			"  --role=roles/iam.workloadIdentityUser \\\n"+
			"  --member=principal://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/%s/subject/%s", poolID, externalID),
	}, "\n")

	return provider.Instructions{
		Title:       "Conectar un proyecto de Google Cloud (próximamente)",
		Description: description,
		Command:     command,
		Fields: []provider.Field{
			{
				Name:        "external_ref",
				Label:       "ID del proyecto",
				Placeholder: "mi-proyecto-123456",
				Help:        "El project ID (no el número ni el nombre).",
				Required:    true,
			},
			{
				Name:        SettingServiceAccountEmail,
				Label:       "Email de la service account",
				Placeholder: "rampa-connect@mi-proyecto-123456.iam.gserviceaccount.com",
				Help:        "La service account que Rampa impersona.",
				Required:    true,
			},
			{
				Name:        SettingWorkloadIdentityProvider,
				Label:       "Workload Identity Provider",
				Placeholder: "projects/123456789/locations/global/workloadIdentityPools/rampa-" + orgSlug + "/providers/rampa",
				Help:        "El nombre completo del provider OIDC dentro del pool.",
				Required:    true,
			},
		},
	}
}

// Verify todavía no está implementado: falta el intercambio de token OIDC por
// credenciales federadas (STS de Google) y la impersonación de la service account.
func (p *Provider) Verify(_ context.Context, acc provider.CloudAccount) (provider.Verification, error) {
	return provider.Verification{}, fmt.Errorf("provider gcp: verificar la cuenta %q (proyecto %q): %w", acc.Name, projectID(acc), provider.ErrNotImplemented)
}

// Registry devuelve el host de Artifact Registry: <región>-docker.pkg.dev/<proyecto>.
func (p *Provider) Registry(acc provider.CloudAccount) (string, error) {
	project := projectID(acc)
	if project == "" {
		return "", fmt.Errorf("provider gcp: la cuenta %q no tiene id de proyecto (external_ref)", acc.Name)
	}
	region := strings.TrimSpace(acc.Region)
	if region == "" {
		return "", fmt.Errorf("provider gcp: la cuenta %q no tiene región; Artifact Registry es regional", acc.Name)
	}
	return fmt.Sprintf("%s-docker.pkg.dev/%s", region, project), nil
}

// SecretStoreSpec arma el ClusterSecretStore hacia Secret Manager con Workload Identity del agente.
func (p *Provider) SecretStoreSpec(acc provider.CloudAccount, cluster provider.ClusterInfo) provider.SecretStoreSpec {
	location := cluster.Region
	if location == "" {
		location = acc.Region
	}
	if location == "" {
		location = DefaultRegion
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
					"rampa.dev/provider":           string(provider.GCP),
				},
			},
			"spec": map[string]any{
				"provider": map[string]any{
					"gcpsm": map[string]any{
						"projectID": projectID(acc),
						"auth": map[string]any{
							"workloadIdentity": map[string]any{
								"clusterLocation": location,
								"clusterName":     cluster.Name,
								"serviceAccountRef": map[string]any{
									"name":      agentServiceAccount,
									"namespace": agentNamespace,
								},
							},
						},
					},
				},
			},
		},
	}
}

// WorkloadIdentityAnnotations devuelve la anotación de Workload Identity de GKE.
func (p *Provider) WorkloadIdentityAnnotations(_ provider.CloudAccount, identity string) map[string]string {
	if strings.TrimSpace(identity) == "" {
		return map[string]string{}
	}
	return map[string]string{"iam.gke.io/gcp-service-account": identity}
}

// ServiceModule apunta a terraform/modules/gcp/<kind>. Ninguno existe todavía.
func (p *Provider) ServiceModule(kind string) (provider.ModuleRef, bool) {
	return provider.ModuleRef{Source: fmt.Sprintf("%s//terraform/modules/gcp/%s?ref=main", p.moduleSource, kind)}, false
}

// projectID lee el proyecto de ExternalRef, con fallback a settings.project_id.
func projectID(acc provider.CloudAccount) string {
	if v := strings.TrimSpace(acc.ExternalRef); v != "" {
		return v
	}
	return strings.TrimSpace(acc.Settings[SettingProjectID])
}
