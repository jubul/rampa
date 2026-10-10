// Package infra ejecuta OpenTofu contra la nube del cliente: genera un root
// module que invoca los módulos embebidos de terraform/modules, asume la
// identidad de la cuenta conectada, corre tofu con log en vivo y guarda el
// estado cifrado en Postgres a través del backend http (docs/api.md,
// "Infraestructura con clicks").
package infra

import (
	"fmt"
	"math"
	"net"
	"regexp"
	"sort"
	"strings"
)

// Tipos de Input. Son los tipos JSON que acepta el formulario; el root module
// los traduce a los tipos de HCL de cada variable.
const (
	InputString = "string"
	InputList   = "list"
	InputBool   = "bool"
	InputNumber = "number"
)

// Kinds de módulo: un Kubernetes administrado o una máquina de un nodo con k3s.
const (
	KindCluster = "cluster"
	KindVM      = "vm"
)

// Input es un dato que el formulario de la web le pide al usuario para un módulo.
type Input struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Help     string   `json:"help,omitempty"`
	Type     string   `json:"type"` // string | list | bool | number
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
	// LabelEN y HelpEN son las versiones en inglés; Localized elige.
	LabelEN string `json:"-"`
	HelpEN  string `json:"-"`

	// check es una validación extra sobre el valor ya tipado.
	check func(v any) error
}

// Localized devuelve una copia del input con label y help en el idioma pedido
// ("es" por defecto, "en" si hay versión en inglés).
func (in Input) Localized(locale string) Input {
	if locale == "en" {
		if in.LabelEN != "" {
			in.Label = in.LabelEN
		}
		if in.HelpEN != "" {
			in.Help = in.HelpEN
		}
	}
	return in
}

// Module describe un módulo de terraform/modules/<provider>/<nombre> que el
// runner sabe ejecutar. Inputs son los campos propios del módulo que se
// muestran en el formulario; name, env, domain, ingress_class y region son
// comunes a todo cluster y van fijos en la request o salen de la cuenta.
type Module struct {
	Provider  string `json:"provider"`
	Kind      string `json:"kind"` // cluster | vm
	Module    string `json:"module"`
	Available bool   `json:"available"`
	// SecretStore es el clusters.secret_store por defecto de los clusters que
	// crea este módulo: kubernetes donde la nube no tiene gestor de secretos.
	SecretStore string  `json:"secret_store,omitempty"`
	Inputs      []Input `json:"inputs"`

	// variables son todas las variables del root module, en orden. Las que el
	// módulo declara (variable "x" en sus .tf) se le pasan como module.<x> = var.<x>.
	variables []variable
	// outputs son los outputs del módulo que el root reexporta, si el módulo los declara.
	outputs []string
	// providers son los required_providers del root module.
	providers []requiredProvider
}

// Path es la ruta del módulo dentro de terraform/modules ("aws/eks-cluster").
func (m Module) Path() string { return m.Module }

// ClusterKind es el clusters.kind de lo que crea el módulo.
func (m Module) ClusterKind() string {
	if m.Kind == KindVM {
		return "vm"
	}
	return "kubernetes"
}

// Variables devuelve los nombres de las variables del root module (para
// filtrar los inputs que van al tfvars).
func (m Module) Variables() []string {
	out := make([]string, 0, len(m.variables))
	for _, v := range m.variables {
		out = append(out, v.Name)
	}
	return out
}

// HasVariable dice si el root module declara la variable.
func (m Module) HasVariable(name string) bool {
	for _, v := range m.variables {
		if v.Name == name {
			return true
		}
	}
	return false
}

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

// DefaultKubernetesVersion es la versión por defecto de un cluster EKS nuevo.
const DefaultKubernetesVersion = "1.33"

// DefaultVPCCIDR es el rango por defecto del módulo aws/eks-cluster.
const DefaultVPCCIDR = "10.42.0.0/16"

// AgentChartPath es la ruta pública donde la API sirve el chart rampa-agent
// empaquetado; los módulos la reciben en agent_chart_url.
const AgentChartPath = "/public/charts/rampa-agent.tgz"

// Opciones de los módulos de VM y de DOKS.
var (
	EC2InstanceTypes = []string{"t3.small", "t3.medium", "t4g.small", "t4g.medium"}
	DropletSizes     = []string{"s-1vcpu-2gb", "s-2vcpu-2gb", "s-2vcpu-4gb", "s-4vcpu-8gb"}
)

var (
	awsProviders = []requiredProvider{
		{Name: "aws", Source: "hashicorp/aws", Version: ">= 6.0"},
		{Name: "tls", Source: "hashicorp/tls", Version: ">= 4.0"},
		{Name: "helm", Source: "hashicorp/helm", Version: ">= 3.0"},
	}
	digitaloceanProviders = []requiredProvider{
		{Name: "digitalocean", Source: "digitalocean/digitalocean", Version: ">= 2.40"},
		{Name: "helm", Source: "hashicorp/helm", Version: ">= 3.0"},
	}
)

var (
	iamPrincipalRe   = regexp.MustCompile(`^arn:aws[a-zA-Z-]*:iam::\d{12}:(user|role)/.+$`)
	route53ZoneRe    = regexp.MustCompile(`^Z[A-Z0-9]{8,32}$`)
	dnsNameRe        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	emailRe          = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	registryNameRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	keyNameRe        = regexp.MustCompile(`^[\w+=,.@ -]{1,255}$`)
	k3sVersionRe     = regexp.MustCompile(`^v\d+\.\d+\.\d+(\+k3s\d+)?$`)
	doksVersionRe    = regexp.MustCompile(`^\d+\.\d+\.\d+-do\.\d+$`)
	sshFingerprintRe = regexp.MustCompile(`^([0-9a-f]{2}:){15}[0-9a-f]{2}$|^SHA256:[A-Za-z0-9+/=]{43,44}$`)
)

// InternalModuleVariables son variables que un módulo embebido declara con
// default y que el runner no pasa ni la web expone: versiones fijas de lo que
// instala el cloud-init, redes internas y opciones de acceso avanzadas. Se
// cambian editando el módulo, no desde un formulario.
var InternalModuleVariables = map[string]bool{
	"helm_version":         true,
	"helm_sha256":          true,
	"cert_manager_version": true,
	"eso_chart_version":    true,
	"pod_cidr":             true,
	"enable_ssm":           true,
	"public_access_cidrs":  true,
	"agent_chart_oci":      true,
	"agent_chart_version":  true,
}

// commonVariables son las variables que reciben todos los módulos (docs/api.md):
// nombre y env del cluster, la API, el bootstrap token (sensible), el chart
// del agente y su imagen, y los tags de la nube (map en AWS, list en DigitalOcean).
func commonVariables(tagsType, tagsDefault string) []variable {
	return []variable{
		{Name: "name", Type: "string"},
		{Name: "env", Type: "string", Default: `"nonprod"`},
		{Name: "api_url", Type: "string", Default: `""`},
		{Name: "bootstrap_token", Type: "string", Default: `""`, Sensitive: true},
		{Name: "agent_chart_url", Type: "string", Default: `""`},
		{Name: "agent_image_repository", Type: "string", Default: `""`},
		{Name: "agent_image_tag", Type: "string", Default: `""`},
		{Name: "tags", Type: tagsType, Default: tagsDefault},
	}
}

func withVariables(common []variable, extra ...variable) []variable {
	out := make([]variable, 0, len(common)+len(extra))
	out = append(out, extra...)
	return append(out, common...)
}

// registry son los módulos conocidos. Los que no están disponibles figuran en
// GET /infra/modules con available false hasta que existan sus módulos.
var registry = []Module{
	{
		Provider: "aws", Kind: KindCluster, Module: "aws/eks-cluster", Available: true, SecretStore: "aws-secrets-manager",
		Inputs: []Input{
			{Name: "kubernetes_version", Label: "Versión de Kubernetes", LabelEN: "Kubernetes version", Type: InputString, Default: DefaultKubernetesVersion, Options: KubernetesVersions, Required: true,
				Help: "EKS Auto Mode requiere 1.29 o superior.", HelpEN: "EKS Auto Mode requires 1.29 or later."},
			{Name: "vpc_cidr", Label: "Rango de la VPC", LabelEN: "VPC CIDR", Type: InputString, Default: DefaultVPCCIDR, check: checkVPCCIDR,
				Help: "Se parte en dos subredes públicas y dos privadas. Cambialo si choca con otra red tuya.", HelpEN: "Split into two public and two private subnets. Change it if it overlaps one of your networks."},
			{Name: "admin_principal_arns", Label: "Identidades IAM con acceso kubectl", LabelEN: "IAM identities with kubectl access", Type: InputList, check: checkEach("admin_principal_arns", checkIAMPrincipal),
				Help: "ARNs de usuarios o roles de tu cuenta que van a poder administrar el cluster. Opcional.", HelpEN: "ARNs of users or roles in your account that will be able to administer the cluster. Optional."},
		},
		variables: withVariables(commonVariables("map(string)", "{}"),
			variable{Name: "region", Type: "string"},
			variable{Name: "kubernetes_version", Type: "string", Default: `"` + DefaultKubernetesVersion + `"`},
			variable{Name: "vpc_cidr", Type: "string", Default: `"` + DefaultVPCCIDR + `"`},
			variable{Name: "admin_principal_arns", Type: "list(string)", Default: "[]"},
			variable{Name: "install_agent", Type: "bool", Default: "true"},
			// agent_token sigue existiendo para este módulo: helm instala el agente
			// con el token del cluster además del bootstrap token.
			variable{Name: "agent_token", Type: "string", Default: `""`, Sensitive: true},
			variable{Name: "agent_chart_path", Type: "string", Default: `""`},
		),
		outputs: []string{
			"cluster_name", "cluster_arn", "cluster_endpoint", "kubernetes_version", "oidc_issuer",
			"agent_role_arn", "agent_installed", "vpc_id", "private_subnet_ids", "public_subnet_ids", "kubeconfig_command",
		},
		providers: awsProviders,
	},
	{
		Provider: "aws", Kind: KindVM, Module: "aws/ec2-k3s", Available: true, SecretStore: "aws-secrets-manager",
		Inputs: []Input{
			{Name: "instance_type", Label: "Tipo de instancia", LabelEN: "Instance type", Type: InputString, Default: "t3.small", Options: EC2InstanceTypes, Required: true,
				Help: "t3.small (2 GB) es el mínimo razonable para k3s. t4g.* son ARM (Graviton), más baratas.", HelpEN: "t3.small (2 GB) is the reasonable minimum for k3s. t4g.* are ARM (Graviton) and cheaper."},
			{Name: "k3s_version", Label: "Versión de k3s", LabelEN: "k3s version", Type: InputString, check: checkOptionalPattern("k3s_version", k3sVersionRe, "v1.31.4+k3s1"),
				Help: "Vacío: la versión fija del módulo.", HelpEN: "Empty: the module's pinned version."},
			{Name: "key_name", Label: "Key pair de EC2 para SSH", LabelEN: "EC2 key pair for SSH", Type: InputString, check: checkOptionalPattern("key_name", keyNameRe, "mi-clave"),
				Help: "Opcional. Sin clave no hay SSH; queda AWS Systems Manager.", HelpEN: "Optional. Without a key there is no SSH; AWS Systems Manager remains."},
			{Name: "admin_cidrs", Label: "CIDRs con acceso SSH", LabelEN: "CIDRs allowed to SSH", Type: InputList, check: checkEach("admin_cidrs", checkCIDR),
				Help: "Rangos que pueden entrar por el puerto 22. Vacío: cerrado.", HelpEN: "Ranges that can reach port 22. Empty: closed."},
			{Name: "route53_zone_id", Label: "Zona de Route 53", LabelEN: "Route 53 zone", Type: InputString, check: checkOptionalPattern("route53_zone_id", route53ZoneRe, "Z0123456789ABCDEFGHIJ"),
				Help: "Opcional. Si la ponés, se crea el registro comodín del dominio.", HelpEN: "Optional. If set, the wildcard DNS record for the domain is created."},
			{Name: "tls_email", Label: "Email para Let's Encrypt", LabelEN: "Email for Let's Encrypt", Type: InputString, check: checkOptionalPattern("tls_email", emailRe, "ops@acme.com"),
				Help: "Opcional. Con email se instala cert-manager y los scopes salen con TLS.", HelpEN: "Optional. With an email, cert-manager is installed and scopes get TLS."},
			{Name: "volume_size_gb", Label: "Disco (GB)", LabelEN: "Disk (GB)", Type: InputNumber, Default: "30", check: checkIntRange("volume_size_gb", 20, 1000)},
		},
		variables: withVariables(commonVariables("map(string)", "{}"),
			variable{Name: "region", Type: "string"},
			variable{Name: "instance_type", Type: "string", Default: `"t3.small"`},
			variable{Name: "k3s_version", Type: "string", Default: `""`},
			variable{Name: "key_name", Type: "string", Default: `""`},
			variable{Name: "admin_cidrs", Type: "list(string)", Default: "[]"},
			variable{Name: "domain", Type: "string", Default: `""`},
			variable{Name: "route53_zone_id", Type: "string", Default: `""`},
			variable{Name: "tls_email", Type: "string", Default: `""`},
			variable{Name: "volume_size_gb", Type: "number", Default: "30"},
		),
		outputs:   []string{"cluster_name", "agent_installed", "instance_id", "public_ip", "agent_role_arn", "ssh_hint", "dns_hint"},
		providers: awsProviders,
	},
	{
		Provider: "digitalocean", Kind: KindCluster, Module: "digitalocean/doks-cluster", Available: true, SecretStore: "kubernetes",
		Inputs: []Input{
			{Name: "kubernetes_version", Label: "Versión de Kubernetes", LabelEN: "Kubernetes version", Type: InputString, check: checkOptionalPattern("kubernetes_version", doksVersionRe, "1.33.1-do.0"),
				Help: "Slug de versión de DOKS. Vacío: la última disponible.", HelpEN: "DOKS version slug. Empty: latest available."},
			{Name: "node_size", Label: "Tamaño de los nodos", LabelEN: "Node size", Type: InputString, Default: "s-2vcpu-4gb", Options: DropletSizes, Required: true},
			{Name: "node_count", Label: "Cantidad de nodos", LabelEN: "Node count", Type: InputNumber, Default: "2", check: checkIntRange("node_count", 1, 20)},
			{Name: "registry_name", Label: "Container Registry (DOCR)", LabelEN: "Container Registry (DOCR)", Type: InputString, check: checkOptionalPattern("registry_name", registryNameRe, "acme"),
				Help: "Opcional. Se crea o usa y queda integrado al cluster para pull sin credenciales.", HelpEN: "Optional. Created or reused, and integrated with the cluster for credential-less pulls."},
		},
		variables: withVariables(commonVariables("list(string)", "[]"),
			variable{Name: "region", Type: "string"},
			variable{Name: "kubernetes_version", Type: "string", Default: `""`},
			variable{Name: "node_size", Type: "string", Default: `"s-2vcpu-4gb"`},
			variable{Name: "node_count", Type: "number", Default: "2"},
			variable{Name: "registry_name", Type: "string", Default: `""`},
		),
		outputs:   []string{"cluster_name", "cluster_id", "endpoint", "kubernetes_version", "agent_installed", "kubeconfig_command", "registry"},
		providers: digitaloceanProviders,
	},
	{
		Provider: "digitalocean", Kind: KindVM, Module: "digitalocean/droplet-k3s", Available: true, SecretStore: "kubernetes",
		Inputs: []Input{
			{Name: "size", Label: "Tamaño del Droplet", LabelEN: "Droplet size", Type: InputString, Default: "s-2vcpu-2gb", Options: DropletSizes, Required: true,
				Help: "k3s necesita al menos 2 GB.", HelpEN: "k3s needs at least 2 GB."},
			{Name: "image", Label: "Imagen", LabelEN: "Image", Type: InputString, Default: "ubuntu-24-04-x64", check: checkOptionalPattern("image", registryNameRe, "ubuntu-24-04-x64")},
			{Name: "k3s_version", Label: "Versión de k3s", LabelEN: "k3s version", Type: InputString, check: checkOptionalPattern("k3s_version", k3sVersionRe, "v1.31.4+k3s1"),
				Help: "Vacío: la versión fija del módulo.", HelpEN: "Empty: the module's pinned version."},
			{Name: "ssh_key_fingerprints", Label: "Claves SSH", LabelEN: "SSH keys", Type: InputList, check: checkEach("ssh_key_fingerprints", checkSSHFingerprint),
				Help: "Fingerprints de claves ya cargadas en DigitalOcean. Vacío: sin SSH.", HelpEN: "Fingerprints of keys already uploaded to DigitalOcean. Empty: no SSH."},
			{Name: "admin_cidrs", Label: "CIDRs con acceso SSH", LabelEN: "CIDRs allowed to SSH", Type: InputList, check: checkEach("admin_cidrs", checkCIDR),
				Help: "Rangos que pueden entrar por el puerto 22. Vacío: cerrado.", HelpEN: "Ranges that can reach port 22. Empty: closed."},
			{Name: "dns_zone", Label: "Zona DNS en DigitalOcean", LabelEN: "DNS zone in DigitalOcean", Type: InputString, check: checkOptionalPattern("dns_zone", dnsNameRe, "acme.com"),
				Help: "Opcional. Si administrás el dominio en DigitalOcean, se crea el registro comodín.", HelpEN: "Optional. If the domain is managed in DigitalOcean, the wildcard record is created."},
			{Name: "tls_email", Label: "Email para Let's Encrypt", LabelEN: "Email for Let's Encrypt", Type: InputString, check: checkOptionalPattern("tls_email", emailRe, "ops@acme.com"),
				Help: "Opcional. Con email se instala cert-manager y los scopes salen con TLS.", HelpEN: "Optional. With an email, cert-manager is installed and scopes get TLS."},
			{Name: "registry_name", Label: "Container Registry (DOCR)", LabelEN: "Container Registry (DOCR)", Type: InputString, check: checkOptionalPattern("registry_name", registryNameRe, "acme"),
				Help: "Opcional. k3s queda configurado para hacer pull de ese registry.", HelpEN: "Optional. k3s is configured to pull from that registry."},
		},
		variables: withVariables(commonVariables("list(string)", "[]"),
			variable{Name: "region", Type: "string"},
			variable{Name: "size", Type: "string", Default: `"s-2vcpu-2gb"`},
			variable{Name: "image", Type: "string", Default: `"ubuntu-24-04-x64"`},
			variable{Name: "k3s_version", Type: "string", Default: `""`},
			variable{Name: "ssh_key_fingerprints", Type: "list(string)", Default: "[]"},
			variable{Name: "admin_cidrs", Type: "list(string)", Default: "[]"},
			variable{Name: "domain", Type: "string", Default: `""`},
			variable{Name: "dns_zone", Type: "string", Default: `""`},
			variable{Name: "tls_email", Type: "string", Default: `""`},
			variable{Name: "registry_name", Type: "string", Default: `""`},
		),
		outputs:   []string{"cluster_name", "agent_installed", "public_ip", "droplet_id", "ssh_hint", "dns_hint", "registry"},
		providers: digitaloceanProviders,
	},
	{Provider: "gcp", Kind: KindCluster, Module: "gcp/gke-cluster", SecretStore: "gcp-secret-manager", Inputs: []Input{}},
	{Provider: "azure", Kind: KindCluster, Module: "azure/aks-cluster", SecretStore: "azure-key-vault", Inputs: []Input{}},
	{Provider: "oci", Kind: KindCluster, Module: "oci/oke-cluster", SecretStore: "oci-vault", Inputs: []Input{}},
	{Provider: "oci", Kind: KindVM, Module: "oci/compute-k3s", SecretStore: "kubernetes", Inputs: []Input{}},
	{Provider: "ovh", Kind: KindCluster, Module: "ovh/kube-cluster", SecretStore: "kubernetes", Inputs: []Input{}},
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

// InputError es un input inválido; el handler lo traduce a 400.
type InputError struct {
	Name string
	Msg  string
}

func (e *InputError) Error() string {
	if e.Name == "" {
		return e.Msg
	}
	return "inputs." + e.Name + ": " + e.Msg
}

func inputErr(name, format string, args ...any) error {
	return &InputError{Name: name, Msg: fmt.Sprintf(format, args...)}
}

// ValidateInputs valida los inputs del formulario contra los declarados por el
// módulo: tipos, opciones, requeridos y defaults. Devuelve el mapa ya tipado,
// listo para el tfvars, con los defaults aplicados. Las claves desconocidas
// son un error para que un typo no pase en silencio.
func ValidateInputs(m Module, raw map[string]any) (map[string]any, error) {
	known := map[string]bool{}
	out := make(map[string]any, len(m.Inputs))
	for _, in := range m.Inputs {
		known[in.Name] = true
		v, present := raw[in.Name]
		if !present || v == nil || (in.Type == InputString && strings.TrimSpace(fmt.Sprint(v)) == "" && v != nil && isString(v)) {
			switch {
			case in.Default != "":
				dv, err := coerceInput(in, in.Default)
				if err != nil {
					return nil, err
				}
				out[in.Name] = dv
			case in.Required:
				return nil, inputErr(in.Name, "es requerido")
			case in.Type == InputList:
				out[in.Name] = []string{}
			case in.Type == InputString:
				out[in.Name] = ""
			case in.Type == InputBool:
				out[in.Name] = false
			}
			continue
		}
		tv, err := coerceInput(in, v)
		if err != nil {
			return nil, err
		}
		if len(in.Options) > 0 {
			sv, _ := tv.(string)
			found := false
			for _, o := range in.Options {
				if o == sv {
					found = true
				}
			}
			if !found {
				return nil, inputErr(in.Name, "%q no es una opción. Opciones: %s", sv, strings.Join(in.Options, ", "))
			}
		}
		if in.check != nil {
			if err := in.check(tv); err != nil {
				return nil, err
			}
		}
		out[in.Name] = tv
	}
	var unknown []string
	for k := range raw {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		names := make([]string, 0, len(m.Inputs))
		for _, in := range m.Inputs {
			names = append(names, in.Name)
		}
		return nil, inputErr("", "el módulo %s no acepta %s. Inputs: %s", m.Module, strings.Join(unknown, ", "), strings.Join(names, ", "))
	}
	return out, nil
}

func isString(v any) bool {
	_, ok := v.(string)
	return ok
}

// coerceInput convierte un valor JSON al tipo del input.
func coerceInput(in Input, v any) (any, error) {
	switch in.Type {
	case InputString:
		s, ok := v.(string)
		if !ok {
			return nil, inputErr(in.Name, "tiene que ser un string")
		}
		return strings.TrimSpace(s), nil
	case InputList:
		switch l := v.(type) {
		case []string:
			out := make([]string, 0, len(l))
			for _, s := range l {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
			return out, nil
		case []any:
			out := make([]string, 0, len(l))
			for _, e := range l {
				s, ok := e.(string)
				if !ok {
					return nil, inputErr(in.Name, "tiene que ser una lista de strings")
				}
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
			return out, nil
		case string:
			// El formulario puede mandar una lista como texto separado por comas.
			out := []string{}
			for _, s := range strings.Split(l, ",") {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
			return out, nil
		default:
			return nil, inputErr(in.Name, "tiene que ser una lista de strings")
		}
	case InputBool:
		switch b := v.(type) {
		case bool:
			return b, nil
		case string:
			switch strings.ToLower(strings.TrimSpace(b)) {
			case "true":
				return true, nil
			case "false":
				return false, nil
			}
		}
		return nil, inputErr(in.Name, "tiene que ser true o false")
	case InputNumber:
		switch n := v.(type) {
		case float64:
			if n != math.Trunc(n) {
				return nil, inputErr(in.Name, "tiene que ser un entero")
			}
			return int(n), nil
		case int:
			return n, nil
		case int64:
			return int(n), nil
		case string:
			var i int
			if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &i); err != nil || fmt.Sprint(i) != strings.TrimSpace(n) {
				return nil, inputErr(in.Name, "tiene que ser un entero")
			}
			return i, nil
		}
		return nil, inputErr(in.Name, "tiene que ser un entero")
	}
	return nil, inputErr(in.Name, "tipo de input desconocido %q", in.Type)
}

// ---- validaciones extra ----

func checkVPCCIDR(v any) error {
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil || ip.To4() == nil {
		return inputErr("vpc_cidr", "debe ser un rango IPv4 como 10.42.0.0/16")
	}
	if ones, _ := ipnet.Mask.Size(); ones < 16 || ones > 24 {
		return inputErr("vpc_cidr", "debe tener un prefijo entre /16 y /24")
	}
	return nil
}

func checkIAMPrincipal(name, s string) error {
	if !iamPrincipalRe.MatchString(s) {
		return inputErr(name, "%q no es el ARN de un usuario o rol IAM", s)
	}
	return nil
}

func checkCIDR(name, s string) error {
	if _, _, err := net.ParseCIDR(s); err != nil {
		return inputErr(name, "%q no es un CIDR válido (por ejemplo 203.0.113.0/24)", s)
	}
	return nil
}

func checkSSHFingerprint(name, s string) error {
	if !sshFingerprintRe.MatchString(s) {
		return inputErr(name, "%q no es un fingerprint de clave SSH (aa:bb:... o SHA256:...)", s)
	}
	return nil
}

// checkEach aplica fn a cada elemento de una lista.
func checkEach(name string, fn func(name, s string) error) func(any) error {
	return func(v any) error {
		l, _ := v.([]string)
		for _, s := range l {
			if err := fn(name, s); err != nil {
				return err
			}
		}
		return nil
	}
}

// checkOptionalPattern acepta vacío o un string que cumpla el patrón.
func checkOptionalPattern(name string, re *regexp.Regexp, example string) func(any) error {
	return func(v any) error {
		s, _ := v.(string)
		if s == "" {
			return nil
		}
		if !re.MatchString(s) {
			return inputErr(name, "%q no tiene el formato esperado (por ejemplo %s)", s, example)
		}
		return nil
	}
}

func checkIntRange(name string, min, max int) func(any) error {
	return func(v any) error {
		n, _ := v.(int)
		if n < min || n > max {
			return inputErr(name, "debe estar entre %d y %d", min, max)
		}
		return nil
	}
}

// Localized devuelve una copia del módulo con sus inputs en el idioma pedido.
func (m Module) Localized(locale string) Module {
	out := m
	out.Inputs = make([]Input, len(m.Inputs))
	for i, in := range m.Inputs {
		out.Inputs[i] = in.Localized(locale)
	}
	return out
}

// LocalizedModules devuelve el registro con los inputs en el idioma pedido.
func LocalizedModules(locale string) []Module {
	all := Modules()
	out := make([]Module, len(all))
	for i, m := range all {
		out[i] = m.Localized(locale)
	}
	return out
}
