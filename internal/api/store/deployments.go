package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const deploymentColumns = `d.id, d.org_id, d.application_id, d.scope_id, d.cluster_id, a.name, sc.name, d.image, d.values, d.status, d.message,
	d.requested_by, d.approved_by, d.approval_id, d.workload, d.started_at, d.finished_at, d.created_at, d.updated_at`

const deploymentFrom = ` FROM deployments d JOIN applications a ON a.id = d.application_id JOIN scopes sc ON sc.id = d.scope_id `

func scanDeployment(row interface{ Scan(dest ...any) error }) (*Deployment, error) {
	var d Deployment
	var requestedBy, approvedBy, approvalID *string
	var values, workload []byte
	if err := row.Scan(&d.ID, &d.OrgID, &d.ApplicationID, &d.ScopeID, &d.ClusterID, &d.AppName, &d.ScopeName, &d.Image, &values, &d.Status, &d.Message,
		&requestedBy, &approvedBy, &approvalID, &workload, &d.StartedAt, &d.FinishedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	d.RequestedBy = ptrString(requestedBy)
	d.ApprovedBy = ptrString(approvedBy)
	d.ApprovalID = ptrString(approvalID)
	d.Values = json.RawMessage(values)
	if len(workload) > 0 && string(workload) != "null" {
		d.Workload = json.RawMessage(workload)
	}
	return &d, nil
}

// CreateDeployment inserta el deployment con el estado inicial que trae.
func (s *Store) CreateDeployment(ctx context.Context, d *Deployment) (*Deployment, error) {
	id := uuid.NewString()
	if d.ID != "" {
		id = d.ID
	}
	values := d.Values
	if len(values) == 0 {
		values = json.RawMessage("{}")
	}
	_, err := s.db.Exec(ctx, `INSERT INTO deployments (id, org_id, application_id, scope_id, cluster_id, image, values, status, message, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, d.OrgID, d.ApplicationID, d.ScopeID, d.ClusterID, d.Image, values, d.Status, d.Message, nullString(d.RequestedBy))
	if err != nil {
		return nil, translate(err)
	}
	return s.GetDeployment(ctx, d.OrgID, id)
}

// GetDeployment busca un deployment de la org.
func (s *Store) GetDeployment(ctx context.Context, orgID, id string) (*Deployment, error) {
	return scanDeployment(s.db.QueryRow(ctx, `SELECT `+deploymentColumns+deploymentFrom+`WHERE d.org_id = $1 AND d.id = $2`, orgID, id))
}

// ListDeploymentsForScope lista del más nuevo al más viejo.
func (s *Store) ListDeploymentsForScope(ctx context.Context, orgID, scopeID string, limit int) ([]Deployment, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `SELECT `+deploymentColumns+deploymentFrom+`WHERE d.org_id = $1 AND d.scope_id = $2 ORDER BY d.created_at DESC LIMIT $3`, orgID, scopeID, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeploymentUpdate son los campos que cambian durante el ciclo de vida.
type DeploymentUpdate struct {
	Status     string
	Message    *string
	Workload   json.RawMessage
	StartedAt  *time.Time
	FinishedAt *time.Time
	ApprovedBy string
	ApprovalID string
}

// UpdateDeployment aplica un DeploymentUpdate. Solo pisa los campos seteados.
func (s *Store) UpdateDeployment(ctx context.Context, orgID, id string, u DeploymentUpdate) (*Deployment, error) {
	var workload any
	if len(u.Workload) > 0 {
		workload = u.Workload
	}
	_, err := s.db.Exec(ctx, `UPDATE deployments SET
		status = CASE WHEN $3 = '' THEN status ELSE $3 END,
		message = COALESCE($4, message),
		workload = COALESCE($5, workload),
		started_at = COALESCE(started_at, $6),
		finished_at = COALESCE($7, finished_at),
		approved_by = COALESCE($8::uuid, approved_by),
		approval_id = COALESCE($9::uuid, approval_id),
		updated_at = now()
		WHERE org_id = $1 AND id = $2`,
		orgID, id, u.Status, u.Message, workload, nullTime(u.StartedAt), nullTime(u.FinishedAt), nullString(u.ApprovedBy), nullString(u.ApprovalID))
	if err != nil {
		return nil, translate(err)
	}
	return s.GetDeployment(ctx, orgID, id)
}

// TransitionDeployment aplica u solo si el deployment está en alguno de los
// estados from. Si no (alguien lo movió antes), devuelve ErrConflict sin tocar
// nada. Es la versión check-and-set de UpdateDeployment para approve y cancel.
func (s *Store) TransitionDeployment(ctx context.Context, orgID, id string, from []string, u DeploymentUpdate) (*Deployment, error) {
	if u.Status == "" {
		return nil, errors.New("TransitionDeployment requiere Status")
	}
	var workload any
	if len(u.Workload) > 0 {
		workload = u.Workload
	}
	tag, err := s.db.Exec(ctx, `UPDATE deployments SET
		status = $3,
		message = COALESCE($4, message),
		workload = COALESCE($5, workload),
		started_at = COALESCE(started_at, $6),
		finished_at = COALESCE($7, finished_at),
		approved_by = COALESCE($8::uuid, approved_by),
		approval_id = COALESCE($9::uuid, approval_id),
		updated_at = now()
		WHERE org_id = $1 AND id = $2 AND status = ANY($10::text[])`,
		orgID, id, u.Status, u.Message, workload, nullTime(u.StartedAt), nullTime(u.FinishedAt), nullString(u.ApprovedBy), nullString(u.ApprovalID), from)
	if err != nil {
		return nil, translate(err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetDeployment(ctx, orgID, id); err != nil {
			return nil, err
		}
		return nil, ErrConflict
	}
	return s.GetDeployment(ctx, orgID, id)
}

// LastHealthyDeploymentForScope devuelve el deployment healthy más reciente
// del scope, excluyendo excludeID. ErrNotFound si no hay ninguno.
func (s *Store) LastHealthyDeploymentForScope(ctx context.Context, orgID, scopeID, excludeID string) (*Deployment, error) {
	return scanDeployment(s.db.QueryRow(ctx, `SELECT `+deploymentColumns+deploymentFrom+`
		WHERE d.org_id = $1 AND d.scope_id = $2 AND d.status = $3 AND d.id <> $4::uuid
		ORDER BY d.finished_at DESC NULLS LAST, d.created_at DESC LIMIT 1`, orgID, scopeID, DeploymentHealthy, excludeID))
}

// ---- approvals ----

// CreateApproval registra que un deployment necesita aprobación.
func (s *Store) CreateApproval(ctx context.Context, orgID, deploymentID string, requiredByPolicy bool) (*Approval, error) {
	var a Approval
	var approvedBy *string
	err := s.db.QueryRow(ctx, `INSERT INTO approvals (id, org_id, deployment_id, required_by_policy) VALUES ($1, $2, $3, $4)
		RETURNING id, org_id, deployment_id, required_by_policy, approved_by, approved_at, created_at`,
		uuid.NewString(), orgID, deploymentID, requiredByPolicy).
		Scan(&a.ID, &a.OrgID, &a.DeploymentID, &a.RequiredByPolicy, &approvedBy, &a.ApprovedAt, &a.CreatedAt)
	if err != nil {
		return nil, translate(err)
	}
	a.ApprovedBy = ptrString(approvedBy)
	return &a, nil
}

// ApproveApproval marca la aprobación como concedida.
func (s *Store) ApproveApproval(ctx context.Context, orgID, deploymentID, userID string) error {
	_, err := s.db.Exec(ctx, `UPDATE approvals SET approved_by = $3, approved_at = now(), updated_at = now()
		WHERE org_id = $1 AND deployment_id = $2 AND approved_at IS NULL`, orgID, deploymentID, nullString(userID))
	return translate(err)
}
