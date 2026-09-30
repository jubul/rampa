// Package provider abstrae lo que difiere entre nubes: cómo se conecta una
// cuenta, dónde está el registry, cómo se leen secretos y con qué identidad
// corren los pods. Kubernetes es el runtime común, así que el resto de Rampa
// no sabe en qué nube está.
//
// Las implementaciones viven en subpaquetes (aws, gcp, azure, digitalocean,
// oci, ovh) y se registran con Register en su init. La API las busca con Get.
package provider

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// Kind identifica la nube.
type Kind string

const (
	AWS          Kind = "aws"
	GCP          Kind = "gcp"
	Azure        Kind = "azure"
	DigitalOcean Kind = "digitalocean"
	OCI          Kind = "oci"
	OVH          Kind = "ovh"
)

// AuthKind dice cómo se conecta una cuenta: por federación de identidad (sin
// secretos de larga duración) o con un token de API del cliente que la API
// guarda cifrado (ADR 0004).
type AuthKind string

const (
	AuthFederated AuthKind = "federated"
	AuthToken     AuthKind = "token"
)

// LocalizedTerm es un término en los dos idiomas de la plataforma.
type LocalizedTerm struct {
	ES string `json:"es"`
	EN string `json:"en"`
}

// Terminology son los nombres que usa cada nube para sus conceptos. La web
// los usa en todos sus textos: en DigitalOcean dice Droplet y DOKS, en Azure
// máquina virtual y AKS.
type Terminology struct {
	Account     LocalizedTerm `json:"account"`
	Project     LocalizedTerm `json:"project"`
	Cluster     LocalizedTerm `json:"cluster"`
	VM          LocalizedTerm `json:"vm"`
	Registry    LocalizedTerm `json:"registry"`
	SecretStore LocalizedTerm `json:"secret_store"`
	Region      LocalizedTerm `json:"region"`
}

// Terms devuelve los términos en orden fijo, para recorrerlos (tests, listados).
func (t Terminology) Terms() map[string]LocalizedTerm {
	return map[string]LocalizedTerm{
		"account": t.Account, "project": t.Project, "cluster": t.Cluster, "vm": t.VM,
		"registry": t.Registry, "secret_store": t.SecretStore, "region": t.Region,
	}
}

// ErrNotImplemented lo devuelven las nubes que todavía no están implementadas
// o las operaciones que una nube no soporta.
var ErrNotImplemented = errors.New("no implementado para este provider")

// CloudAccount es una cuenta de nube conectada a una organización. Para las
// nubes federadas nunca contiene credenciales de larga duración: solo lo
// necesario para asumir una identidad. Para las nubes con auth token, la API
// carga Credentials en memoria desde el cifrado justo antes de llamar a Verify
// o a Credentials; nunca se serializa ni se persiste en texto plano.
type CloudAccount struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
	Kind  Kind   `json:"kind"`
	Name  string `json:"name"`
	// ExternalRef identifica la cuenta en la nube:
	//   aws:   ARN del rol IAM que Rampa asume
	//   gcp:   id del proyecto
	//   azure: id de la suscripción
	ExternalRef string `json:"external_ref"`
	Region      string `json:"region"`
	// Settings guarda lo específico de cada nube:
	//   aws:   external_id
	//   gcp:   workload_identity_provider, service_account_email
	//   azure: tenant_id, client_id
	//   digitalocean: registry_name
	Settings map[string]string `json:"settings"`
	// Credentials son los campos marcados Field.Secret (por ejemplo api_token),
	// descifrados. Solo viven en memoria durante una llamada al provider.
	Credentials map[string]string `json:"-"`
}

// ClusterInfo es lo que el provider necesita saber de un cluster.
type ClusterInfo struct {
	Name       string
	Region     string
	OIDCIssuer string // issuer del cluster, para identidad de pods
}

// ConnectRequest es lo que la API sabe de una cuenta al momento de armar las
// instrucciones de conexión.
type ConnectRequest struct {
	OrgSlug     string // slug de la organización, para nombres legibles
	ExternalID  string // único por cuenta, lo genera la API (confused deputy)
	CallbackURL string // URL pública de la API, para plantillas y redirects
	Region      string // región elegida al crear la cuenta; vacío usa el default del provider
}

// Field es un dato que el cliente tiene que pegar en el formulario de conexión.
type Field struct {
	Name        string `json:"name"` // clave en CloudAccount.ExternalRef ("external_ref") o Settings
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
	Required    bool   `json:"required"`
	// Secret marca un campo que la API cifra (cloud_accounts.credentials_ciphertext)
	// y nunca devuelve. Va a CloudAccount.Credentials, no a Settings.
	Secret bool `json:"secret"`
}

// Instructions es lo que se le muestra al cliente para conectar su cuenta.
type Instructions struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	URL         string  `json:"url,omitempty"`     // link de un click (CloudFormation quick-create, consola)
	Command     string  `json:"command,omitempty"` // alternativa por CLI
	Fields      []Field `json:"fields"`            // qué tiene que devolvernos el cliente
}

// Verification es el resultado de comprobar una cuenta.
type Verification struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity"` // quién somos en la nube del cliente
	Message  string `json:"message"`
}

// SecretStoreSpec es el ClusterSecretStore de External Secrets que el agente
// instala para leer el secret manager de la nube.
type SecretStoreSpec struct {
	Name   string         `json:"name"`
	Object map[string]any `json:"object"` // manifiesto completo
}

// ModuleRef apunta al módulo OpenTofu de un service.
type ModuleRef struct {
	Source string `json:"source"` // git::https://github.com/<org>/rampa.git//terraform/modules/aws/postgres
}

// Provider es lo que Rampa necesita de una nube.
type Provider interface {
	Kind() Kind
	// DisplayName es el nombre para humanos: "Amazon Web Services".
	DisplayName() string
	// Available dice si la implementación está completa. Las que no lo están
	// igual se listan en la web como "próximamente".
	Available() bool
	// Auth dice si la cuenta se conecta por federación o con un token del cliente.
	Auth() AuthKind
	// Terminology son los nombres que usa esta nube para cuenta, cluster, VM,
	// registry, gestor de secretos y región, en español e inglés.
	Terminology() Terminology
	// ConnectInstructions arma lo que el cliente tiene que hacer para conectar
	// una cuenta a partir de lo que la API ya sabe de ella (ConnectRequest).
	ConnectInstructions(req ConnectRequest) Instructions
	// Verify comprueba que podemos asumir la identidad en la cuenta del cliente.
	// Convención: devuelve error solo si no se pudo intentar (configuración
	// faltante, red); si la nube rechaza, devuelve Verification{OK: false,
	// Message} sin error.
	Verify(ctx context.Context, acc CloudAccount) (Verification, error)
	// Registry devuelve el host del registry. La imagen de una app es <registry>/rampa/<app>.
	Registry(acc CloudAccount) (string, error)
	// SecretStoreSpec arma el ClusterSecretStore para un cluster.
	SecretStoreSpec(acc CloudAccount, cluster ClusterInfo) SecretStoreSpec
	// WorkloadIdentityAnnotations son las anotaciones del ServiceAccount para
	// que los pods asuman identity (un ARN de rol, un email de SA, un client id).
	WorkloadIdentityAnnotations(acc CloudAccount, identity string) map[string]string
	// ServiceModule devuelve el módulo OpenTofu para un tipo de service (postgres, redis...).
	ServiceModule(kind string) (ModuleRef, bool)
}

var (
	mu       sync.RWMutex
	registry = map[Kind]Provider{}
)

// Register agrega un provider. Lo llaman los subpaquetes en su init.
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	registry[p.Kind()] = p
}

// Get devuelve un provider por kind.
func Get(kind Kind) (Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[kind]
	return p, ok
}

// All devuelve los providers registrados, ordenados por kind.
func All() []Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Provider, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}
