package apiclient

import "time"

// Los tipos siguen docs/api.md. Solo los campos que el CLI usa o muestra.

// User es una identidad global.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Org es una organización.
type Org struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Membership es el rol de un usuario en una organización.
type Membership struct {
	Org  Org    `json:"org"`
	Role string `json:"role"`
}

// Me es la respuesta de GET /v1/me.
type Me struct {
	User        User         `json:"user"`
	Memberships []Membership `json:"memberships"`
}

// Application es una app importada.
type Application struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Team           string    `json:"team"`
	Tier           int       `json:"tier"`
	Description    string    `json:"description"`
	Type           string    `json:"type"`
	Runtime        string    `json:"runtime"`
	RepoURL        string    `json:"repo_url"`
	ManifestSHA    string    `json:"manifest_sha"`
	ScopesDeclared []string  `json:"scopes_declared"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Scope es un scope con su estado en runtime.
type Scope struct {
	Name              string      `json:"name"`
	Ephemeral         bool        `json:"ephemeral"`
	Declared          bool        `json:"declared"`
	ClusterID         string      `json:"cluster_id"`
	Namespace         string      `json:"namespace"`
	URL               string      `json:"url"`
	Status            string      `json:"status"`
	CurrentDeployment *Deployment `json:"current_deployment"`
	Size              string      `json:"size"`
	Replicas          int         `json:"replicas"`
	Strategy          string      `json:"strategy"`
	TTLExpiresAt      *time.Time  `json:"ttl_expires_at"`
}

// Deployment es una imagen llevada a un scope.
type Deployment struct {
	ID          string     `json:"id"`
	App         string     `json:"app"`
	Scope       string     `json:"scope"`
	ClusterID   string     `json:"cluster_id"`
	Image       string     `json:"image"`
	Status      string     `json:"status"`
	Message     string     `json:"message"`
	RequestedBy *User      `json:"requested_by"`
	ApprovedBy  *User      `json:"approved_by"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// Estados terminales de un deployment.
const (
	DeploymentHealthy   = "healthy"
	DeploymentFailed    = "failed"
	DeploymentCancelled = "cancelled"
)

// IsTerminal dice si el deployment ya no va a cambiar de estado.
func (d *Deployment) IsTerminal() bool {
	if d == nil {
		return false
	}
	switch d.Status {
	case DeploymentHealthy, DeploymentFailed, DeploymentCancelled:
		return true
	}
	return false
}

// Actor es quién generó un evento.
type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Event es un evento de la organización, tal como llega por SSE o por GET /events.
type Event struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Actor       Actor          `json:"actor"`
	SubjectType string         `json:"subject_type"`
	SubjectID   string         `json:"subject_id"`
	Message     string         `json:"message"`
	Payload     map[string]any `json:"payload"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Kinds de eventos de deployment que cierran una espera.
const (
	EventDeploymentHealthy   = "deployment.healthy"
	EventDeploymentFailed    = "deployment.failed"
	EventDeploymentCancelled = "deployment.cancelled"
)

type list[T any] struct {
	Items []T `json:"items"`
}

// CreateAppRequest es el cuerpo de POST /apps.
type CreateAppRequest struct {
	Manifest string `json:"manifest"`
	RepoURL  string `json:"repo_url,omitempty"`
}

// PutManifestRequest es el cuerpo de PUT /apps/{app}/manifest.
type PutManifestRequest struct {
	Manifest string `json:"manifest"`
}

// BindScopeRequest es el cuerpo de PUT /apps/{app}/scopes/{scope}.
type BindScopeRequest struct {
	ClusterID string `json:"cluster_id"`
}

// CreateDeploymentRequest es el cuerpo de POST .../deployments.
type CreateDeploymentRequest struct {
	Image   string `json:"image"`
	Message string `json:"message,omitempty"`
}
