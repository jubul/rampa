package store

import (
	"encoding/json"
	"time"
)

// Roles por organización, de mayor a menor.
const (
	RoleOwner     = "owner"
	RoleAdmin     = "admin"
	RoleDeveloper = "developer"
	RoleViewer    = "viewer"
)

// Estados de un deployment (docs/api.md).
const (
	DeploymentAwaitingApproval = "awaiting_approval"
	DeploymentQueued           = "queued"
	DeploymentApplying         = "applying"
	DeploymentProgressing      = "progressing"
	DeploymentHealthy          = "healthy"
	DeploymentFailed           = "failed"
	DeploymentCancelled        = "cancelled"
)

// Estados de un scope (docs/api.md).
const (
	ScopeUnbound   = "unbound"
	ScopeIdle      = "idle"
	ScopeDeploying = "deploying"
	ScopeHealthy   = "healthy"
	ScopeDegraded  = "degraded"
	ScopeFailed    = "failed"
	ScopeDeleting  = "deleting"
)

// Estados de un agent_command.
const (
	CommandQueued = "queued"
	CommandSent   = "sent"
	CommandAcked  = "acked"
	CommandDone   = "done"
	CommandFailed = "failed"
)

// Estados de cluster y cuenta de nube.
const (
	ClusterOnline  = "online"
	ClusterOffline = "offline"
	// ClusterFailed es un cluster cuyo run de infraestructura falló: existe en
	// Rampa con su infra_run_id para reintentar o borrar.
	ClusterFailed = "failed"

	CloudAccountPending   = "pending"
	CloudAccountConnected = "connected"
	CloudAccountFailed    = "failed"
)

// Envs de cluster.
const (
	EnvProd    = "prod"
	EnvNonprod = "nonprod"
)

// Kinds de cluster (ADR 0004): un Kubernetes administrado o registrado, o una
// máquina de un solo nodo con k3s creada por Rampa.
const (
	ClusterKindKubernetes = "kubernetes"
	ClusterKindVM         = "vm"
)

// SecretStoreKubernetes es el valor especial de clusters.secret_store para
// nubes sin gestor de secretos: el agente mantiene el Secret <app>-params.
const SecretStoreKubernetes = "kubernetes"

// Estados de un run de infraestructura (docs/api.md): apply pasa por
// queued, initializing, planning y applying hasta succeeded o failed; destroy
// pasa por destroying hasta destroyed o failed.
const (
	InfraRunQueued       = "queued"
	InfraRunInitializing = "initializing"
	InfraRunPlanning     = "planning"
	InfraRunApplying     = "applying"
	InfraRunSucceeded    = "succeeded"
	InfraRunFailed       = "failed"
	InfraRunDestroying   = "destroying"
	InfraRunDestroyed    = "destroyed"
)

// Operaciones de un run de infraestructura.
const (
	InfraOpApply   = "apply"
	InfraOpDestroy = "destroy"
)

// Kinds de run de infraestructura: un Kubernetes administrado o una máquina
// con k3s. Los dos crean un Cluster en Rampa.
const (
	InfraKindCluster = "cluster"
	InfraKindVM      = "vm"
)

// OrgSettings son las políticas de una organización.
type OrgSettings struct {
	RequireApprovalProd bool `json:"require_approval_prod"`
	// GitHubOrg es la organización de GitHub donde vive el fork o copia de
	// rampa con los workflows reusables; va en los workflows del scaffold.
	GitHubOrg string `json:"github_org,omitempty"`
}

// Organization es el cliente.
type Organization struct {
	ID        string      `json:"id"`
	Slug      string      `json:"slug"`
	Name      string      `json:"name"`
	Plan      string      `json:"-"`
	Settings  OrgSettings `json:"settings"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"-"`
}

// User es una identidad global.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	// Locale es el idioma preferido ("es", "en") o "" para usar Accept-Language.
	Locale    string    `json:"locale"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// Membership es el rol de un usuario en una organización.
type Membership struct {
	OrgID     string    `json:"-"`
	UserID    string    `json:"-"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"-"`
}

// MembershipWithOrg es una membresía con la organización cargada (para Me).
type MembershipWithOrg struct {
	Org  Organization `json:"org"`
	Role string       `json:"role"`
}

// MemberWithUser es una membresía con el usuario cargado (para listar miembros).
type MemberWithUser struct {
	User User   `json:"user"`
	Role string `json:"role"`
}

// APIToken es un token de CLI o CI. El secreto solo existe al crearlo.
type APIToken struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"-"`
	UserID     string     `json:"-"`
	Name       string     `json:"name"`
	Hash       string     `json:"-"`
	Role       string     `json:"role"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"-"`
}

// Expired dice si el token venció.
func (t *APIToken) Expired(now time.Time) bool {
	return t.ExpiresAt != nil && !now.Before(*t.ExpiresAt)
}

// CloudAccount es una cuenta de nube conectada. Nunca contiene credenciales en
// claro: las de los providers con auth token viven cifradas en
// credentials_ciphertext y solo se descifran en memoria para llamar a la nube.
type CloudAccount struct {
	ID           string            `json:"id"`
	OrgID        string            `json:"-"`
	Provider     string            `json:"provider"`
	Name         string            `json:"name"`
	Region       string            `json:"region"`
	ExternalRef  string            `json:"external_ref"`
	Settings     map[string]string `json:"settings"`
	Status       string            `json:"status"`
	Verification *Verification     `json:"verification"`
	// HasCredentials dice si hay credenciales cifradas guardadas.
	HasCredentials bool      `json:"has_credentials"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"-"`
}

// Verification es el resultado de la última verificación de una cuenta.
type Verification struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity"`
	Message  string `json:"message"`
}

// Cluster es un cluster de Kubernetes con un agente: administrado, registrado
// a mano o una máquina de un nodo con k3s (Kind vm).
type Cluster struct {
	ID                string     `json:"id"`
	OrgID             string     `json:"-"`
	CloudAccountID    string     `json:"cloud_account_id"`
	Name              string     `json:"name"`
	Kind              string     `json:"kind"`
	Env               string     `json:"env"`
	TokenHash         string     `json:"-"`
	AgentVersion      string     `json:"agent_version"`
	KubernetesVersion string     `json:"kubernetes_version"`
	LastSeenAt        *time.Time `json:"last_seen_at"`
	Status            string     `json:"status"`
	Domain            string     `json:"domain"`
	IngressClass      string     `json:"ingress_class"`
	SecretStore       string     `json:"secret_store"`
	// AgentRoleARN es la identidad de nube del agente (IRSA en AWS). La deja
	// el run de infraestructura o la trae el cliente al registrar el cluster.
	AgentRoleARN string `json:"agent_role_arn"`
	// InfraRunID enlaza al run que creó (o intentó crear) el cluster.
	InfraRunID string    `json:"infra_run_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"-"`
}

// BootstrapToken es un token de un solo uso para instalar el agente donde el
// token real no debe quedar escrito (cloud-init). Solo se guarda su hash.
type BootstrapToken struct {
	ID        string     `json:"id"`
	OrgID     string     `json:"-"`
	ClusterID string     `json:"cluster_id"`
	TokenHash string     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedBy string     `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
}

// InfraRun es una ejecución de OpenTofu contra la cuenta de un cliente.
type InfraRun struct {
	ID             string          `json:"id"`
	OrgID          string          `json:"-"`
	Kind           string          `json:"kind"`
	Provider       string          `json:"provider"`
	Module         string          `json:"module"`
	CloudAccountID string          `json:"cloud_account_id"`
	ClusterID      string          `json:"cluster_id"`
	Name           string          `json:"name"`
	Operation      string          `json:"operation"`
	Status         string          `json:"status"`
	Inputs         json.RawMessage `json:"inputs"`
	Outputs        json.RawMessage `json:"outputs"`
	Error          string          `json:"error"`
	StateTokenHash string          `json:"-"`
	RequestedBy    string          `json:"-"`
	Attempt        int             `json:"attempt"`
	LockedAt       *time.Time      `json:"-"`
	StartedAt      *time.Time      `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"-"`
}

// CreatesCluster dice si el run crea un Cluster en Rampa (cluster o vm).
func (r *InfraRun) CreatesCluster() bool {
	return (r.Kind == InfraKindCluster || r.Kind == InfraKindVM) && r.ClusterID != ""
}

// Terminal dice si el run ya no cambia.
func (r *InfraRun) Terminal() bool {
	switch r.Status {
	case InfraRunSucceeded, InfraRunFailed, InfraRunDestroyed:
		return true
	}
	return false
}

// InfraRunLog es una línea del log de un run.
type InfraRunLog struct {
	RunID     string    `json:"-"`
	Seq       int       `json:"seq"`
	Line      string    `json:"line"`
	CreatedAt time.Time `json:"created_at"`
}

// InfraState es el estado de OpenTofu de un run, cifrado, más el lock.
type InfraState struct {
	RunID      string
	Ciphertext []byte
	LockInfo   json.RawMessage // nil si no está tomado
	UpdatedAt  time.Time
}

// Application es un rampa.yaml importado.
type Application struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"-"`
	Name        string    `json:"name"`
	Team        string    `json:"team"`
	Tier        int       `json:"tier"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	Runtime     string    `json:"runtime"`
	RepoURL     string    `json:"repo_url"`
	Manifest    string    `json:"manifest"`
	ManifestSHA string    `json:"manifest_sha"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Scope es el estado en runtime de un scope. Los declarados en el manifest sin
// fila todavía no existen acá.
type Scope struct {
	ID                  string     `json:"id"`
	OrgID               string     `json:"-"`
	ApplicationID       string     `json:"-"`
	Name                string     `json:"name"`
	ClusterID           string     `json:"cluster_id"`
	Ephemeral           bool       `json:"ephemeral"`
	Status              string     `json:"status"`
	CurrentDeploymentID string     `json:"-"`
	TTLExpiresAt        *time.Time `json:"ttl_expires_at"`
	CreatedAt           time.Time  `json:"-"`
	UpdatedAt           time.Time  `json:"-"`
}

// Deployment es una imagen llevada a un scope.
type Deployment struct {
	ID            string          `json:"id"`
	OrgID         string          `json:"-"`
	ApplicationID string          `json:"-"`
	ScopeID       string          `json:"-"`
	ClusterID     string          `json:"cluster_id"`
	AppName       string          `json:"app"`
	ScopeName     string          `json:"scope"`
	Image         string          `json:"image"`
	Values        json.RawMessage `json:"-"`
	Status        string          `json:"status"`
	Message       string          `json:"message"`
	RequestedBy   string          `json:"-"`
	ApprovedBy    string          `json:"-"`
	ApprovalID    string          `json:"-"`
	Workload      json.RawMessage `json:"-"`
	StartedAt     *time.Time      `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"-"`
}

// Terminal dice si el deployment ya no cambia.
func (d *Deployment) Terminal() bool {
	switch d.Status {
	case DeploymentHealthy, DeploymentFailed, DeploymentCancelled:
		return true
	}
	return false
}

// Approval es el registro de aprobación de un deployment a prod.
type Approval struct {
	ID               string     `json:"id"`
	OrgID            string     `json:"-"`
	DeploymentID     string     `json:"deployment_id"`
	RequiredByPolicy bool       `json:"required_by_policy"`
	ApprovedBy       string     `json:"approved_by"`
	ApprovedAt       *time.Time `json:"approved_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

// Actor es quién generó un evento.
type Actor struct {
	Type string `json:"type"` // user | token | agent | system
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Event es una entrada de auditoría y timeline.
type Event struct {
	ID          string          `json:"id"`
	OrgID       string          `json:"-"`
	Kind        string          `json:"kind"`
	Actor       Actor           `json:"actor"`
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	Message     string          `json:"message"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AgentCommand es una entrada de la cola durable hacia un agente.
type AgentCommand struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"-"`
	ClusterID    string          `json:"cluster_id"`
	Kind         string          `json:"kind"`
	Payload      json.RawMessage `json:"payload"`
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result"`
	DeploymentID string          `json:"deployment_id"`
	ScopeID      string          `json:"scope_id"`
	SentAt       *time.Time      `json:"sent_at"`
	AckedAt      *time.Time      `json:"acked_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"-"`
}
