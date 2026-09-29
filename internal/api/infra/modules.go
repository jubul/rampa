// Package infra ejecuta OpenTofu contra la nube del cliente: genera un root
// module que invoca los módulos embebidos de terraform/modules, asume la
// identidad de la cuenta conectada, corre tofu con log en vivo y guarda el
// estado cifrado en Postgres a través del backend http (docs/api.md,
// "Infraestructura con clicks").
package infra

import (
	"sort"
)

// Input es un dato que el formulario de la web le pide al usuario para un módulo.
type Input struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Help     string   `json:"help,omitempty"`
	Type     string   `json:"type"` // string | list | bool
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
}

// Module describe un módulo de terraform/modules/<provider>/<nombre> que el
// runner sabe ejecutar. Inputs son los campos propios del módulo que se
// muestran en el formulario; name, env, domain e ingress_class son comunes a
// todo cluster y van fijos en la request.
type Module struct {
	Provider  string  `json:"provider"`
	Kind      string  `json:"kind"`
	Module    string  `json:"module"`
	Available bool    `json:"available"`
	Inputs    []Input `json:"inputs"`

	// variables son todas las variables del root module, en orden. Cada una se
	// pasa tal cual al módulo (module.<x> = var.<x>).
	variables []variable
	// outputs son los outputs del módulo que el root reexporta.
	outputs []string
	// providers son los required_providers del root module.
	providers []requiredProvider
}

// Path es la ruta del módulo dentro de terraform/modules ("aws/eks-cluster").
func (m Module) Path() string { return m.Module }

// variable es una variable del root module. Default es un literal HCL ya
// formateado ("\"1.33\"", "[]", "true"); vacío significa requerida.
type variable struct {
	Name      string
	Type      string
	Default   string
	Sensitive bool
}

type requiredProvider struct {
	Name, Source, Version string
}

// KubernetesVersions son las versiones que ofrece el formulario para EKS.
var KubernetesVersions = []string{"1.31", "1.32", "1.33", "1.34"}

// DefaultKubernetesVersion es la versión por defecto de un cluster nuevo.
const DefaultKubernetesVersion = "1.33"

// DefaultVPCCIDR es el rango por defecto del módulo aws/eks-cluster.
const DefaultVPCCIDR = "10.42.0.0/16"

var awsProviders = []requiredProvider{
	{Name: "aws", Source: "hashicorp/aws", Version: ">= 6.0"},
	{Name: "tls", Source: "hashicorp/tls", Version: ">= 4.0"},
	{Name: "helm", Source: "hashicorp/helm", Version: ">= 3.0"},
}

// registry son los módulos conocidos. Los que no están disponibles figuran en
// GET /infra/modules con available false hasta que existan sus módulos.
var registry = []Module{
	{
		Provider: "aws", Kind: "cluster", Module: "aws/eks-cluster", Available: true,
		Inputs: []Input{
			{Name: "kubernetes_version", Label: "Versión de Kubernetes", Type: "string", Default: DefaultKubernetesVersion, Options: KubernetesVersions, Required: true,
				Help: "EKS Auto Mode requiere 1.29 o superior."},
			{Name: "vpc_cidr", Label: "Rango de la VPC", Type: "string", Default: DefaultVPCCIDR,
				Help: "Se parte en dos subredes públicas y dos privadas. Cambialo si choca con otra red tuya."},
			{Name: "admin_principal_arns", Label: "Identidades IAM con acceso kubectl", Type: "list",
				Help: "ARNs de usuarios o roles de tu cuenta que van a poder administrar el cluster. Opcional."},
		},
		variables: []variable{
			{Name: "region", Type: "string"},
			{Name: "name", Type: "string"},
			{Name: "env", Type: "string", Default: `"nonprod"`},
			{Name: "kubernetes_version", Type: "string", Default: `"` + DefaultKubernetesVersion + `"`},
			{Name: "vpc_cidr", Type: "string", Default: `"` + DefaultVPCCIDR + `"`},
			{Name: "admin_principal_arns", Type: "list(string)", Default: "[]"},
			{Name: "install_agent", Type: "bool", Default: "true"},
			{Name: "api_url", Type: "string", Default: `""`},
			{Name: "agent_token", Type: "string", Default: `""`, Sensitive: true},
			{Name: "agent_chart_path", Type: "string", Default: `""`},
			{Name: "agent_image_repository", Type: "string", Default: `""`},
			{Name: "agent_image_tag", Type: "string", Default: `""`},
		},
		outputs: []string{
			"cluster_name", "cluster_arn", "cluster_endpoint", "kubernetes_version", "oidc_issuer",
			"agent_role_arn", "agent_installed", "vpc_id", "private_subnet_ids", "public_subnet_ids", "kubeconfig_command",
		},
		providers: awsProviders,
	},
	{Provider: "gcp", Kind: "cluster", Module: "gcp/gke-cluster", Inputs: []Input{}},
	{Provider: "azure", Kind: "cluster", Module: "azure/aks-cluster", Inputs: []Input{}},
	{Provider: "digitalocean", Kind: "cluster", Module: "digitalocean/doks-cluster", Inputs: []Input{}},
	{Provider: "oci", Kind: "cluster", Module: "oci/oke-cluster", Inputs: []Input{}},
}

// Modules devuelve los módulos conocidos, disponibles primero y después por provider.
func Modules() []Module {
	out := make([]Module, len(registry))
	copy(out, registry)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Available != out[j].Available {
			return out[i].Available
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}

// FindModule busca el módulo de un provider y kind.
func FindModule(providerKind, kind string) (Module, bool) {
	for _, m := range registry {
		if m.Provider == providerKind && m.Kind == kind {
			return m, true
		}
	}
	return Module{}, false
}

// FindModuleByPath busca un módulo por su ruta ("aws/eks-cluster").
func FindModuleByPath(path string) (Module, bool) {
	for _, m := range registry {
		if m.Module == path {
			return m, true
		}
	}
	return Module{}, false
}
