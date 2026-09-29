package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const appColumns = "id, org_id, name, team, tier, description, type, runtime, repo_url, manifest, manifest_sha, created_at, updated_at"

func scanApp(row interface{ Scan(dest ...any) error }) (*Application, error) {
	var a Application
	if err := row.Scan(&a.ID, &a.OrgID, &a.Name, &a.Team, &a.Tier, &a.Description, &a.Type, &a.Runtime, &a.RepoURL, &a.Manifest, &a.ManifestSHA, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	return &a, nil
}

// CreateApplication importa una app. ErrConflict si el nombre ya existe en la org.
func (s *Store) CreateApplication(ctx context.Context, a *Application) (*Application, error) {
	return scanApp(s.db.QueryRow(ctx, `INSERT INTO applications (id, org_id, name, team, tier, description, type, runtime, repo_url, manifest, manifest_sha)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING `+appColumns,
		uuid.NewString(), a.OrgID, a.Name, a.Team, a.Tier, a.Description, a.Type, a.Runtime, a.RepoURL, a.Manifest, a.ManifestSHA))
}

// UpdateApplicationManifest reemplaza el manifest y los campos derivados.
func (s *Store) UpdateApplicationManifest(ctx context.Context, a *Application) (*Application, error) {
	return scanApp(s.db.QueryRow(ctx, `UPDATE applications SET team = $3, tier = $4, description = $5, type = $6, runtime = $7,
		repo_url = CASE WHEN $8 = '' THEN repo_url ELSE $8 END, manifest = $9, manifest_sha = $10, updated_at = now()
		WHERE org_id = $1 AND id = $2 RETURNING `+appColumns,
		a.OrgID, a.ID, a.Team, a.Tier, a.Description, a.Type, a.Runtime, a.RepoURL, a.Manifest, a.ManifestSHA))
}

// GetApplicationByName busca una app por nombre dentro de la org.
func (s *Store) GetApplicationByName(ctx context.Context, orgID, name string) (*Application, error) {
	return scanApp(s.db.QueryRow(ctx, `SELECT `+appColumns+` FROM applications WHERE org_id = $1 AND name = $2`, orgID, name))
}

// GetApplication busca una app por id dentro de la org.
func (s *Store) GetApplication(ctx context.Context, orgID, id string) (*Application, error) {
	return scanApp(s.db.QueryRow(ctx, `SELECT `+appColumns+` FROM applications WHERE org_id = $1 AND id = $2`, orgID, id))
}

// ListApplications lista las apps de la org.
func (s *Store) ListApplications(ctx context.Context, orgID string) ([]Application, error) {
	rows, err := s.db.Query(ctx, `SELECT `+appColumns+` FROM applications WHERE org_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []Application{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// DeleteApplication borra la app. ErrConflict si tiene scopes con algo desplegado.
// Corre en una transacción y bloquea la fila de la app (FOR UPDATE) antes de
// mirar los scopes: un deploy concurrente, que inserta o mueve un scope de la
// app, espera a que terminemos o nos hace ver su scope y devolver conflicto.
func (s *Store) DeleteApplication(ctx context.Context, orgID, id string) error {
	return s.WithTx(ctx, func(tx *Store) error {
		var locked string
		if err := tx.db.QueryRow(ctx, `SELECT id FROM applications WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, id).Scan(&locked); err != nil {
			return translate(err)
		}
		var n int
		if err := tx.db.QueryRow(ctx, `SELECT count(*) FROM scopes WHERE org_id = $1 AND application_id = $2 AND status NOT IN ($3, $4)`,
			orgID, id, ScopeUnbound, ScopeIdle).Scan(&n); err != nil {
			return translate(err)
		}
		if n > 0 {
			return ErrConflict
		}
		tag, err := tx.db.Exec(ctx, `DELETE FROM applications WHERE org_id = $1 AND id = $2`, orgID, id)
		if err != nil {
			return translate(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ---- scopes ----

const scopeColumns = "id, org_id, application_id, name, cluster_id, ephemeral, status, current_deployment_id, ttl_expires_at, created_at, updated_at"

func scanScope(row interface{ Scan(dest ...any) error }) (*Scope, error) {
	var sc Scope
	var clusterID, currentDeployment *string
	if err := row.Scan(&sc.ID, &sc.OrgID, &sc.ApplicationID, &sc.Name, &clusterID, &sc.Ephemeral, &sc.Status, &currentDeployment, &sc.TTLExpiresAt, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	sc.ClusterID = ptrString(clusterID)
	sc.CurrentDeploymentID = ptrString(currentDeployment)
	return &sc, nil
}

// UpsertScope crea el scope o le cambia el cluster. Si ya existía, conserva
// estado y deployment actual.
func (s *Store) UpsertScope(ctx context.Context, orgID, appID, name, clusterID string, ephemeral bool, ttl *time.Time) (*Scope, error) {
	status := ScopeUnbound
	if clusterID != "" {
		status = ScopeIdle
	}
	return scanScope(s.db.QueryRow(ctx, `INSERT INTO scopes (id, org_id, application_id, name, cluster_id, ephemeral, status, ttl_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (org_id, application_id, name) DO UPDATE SET
			cluster_id = EXCLUDED.cluster_id,
			status = CASE WHEN scopes.status = 'unbound' THEN EXCLUDED.status ELSE scopes.status END,
			ttl_expires_at = COALESCE(EXCLUDED.ttl_expires_at, scopes.ttl_expires_at),
			updated_at = now()
		RETURNING `+scopeColumns,
		uuid.NewString(), orgID, appID, name, nullString(clusterID), ephemeral, status, nullTime(ttl)))
}

// GetScope busca un scope por nombre dentro de una app.
func (s *Store) GetScope(ctx context.Context, orgID, appID, name string) (*Scope, error) {
	return scanScope(s.db.QueryRow(ctx, `SELECT `+scopeColumns+` FROM scopes WHERE org_id = $1 AND application_id = $2 AND name = $3`, orgID, appID, name))
}

// GetScopeByID busca un scope por id.
func (s *Store) GetScopeByID(ctx context.Context, orgID, id string) (*Scope, error) {
	return scanScope(s.db.QueryRow(ctx, `SELECT `+scopeColumns+` FROM scopes WHERE org_id = $1 AND id = $2`, orgID, id))
}

// ListScopes lista los scopes con fila de una app.
func (s *Store) ListScopes(ctx context.Context, orgID, appID string) ([]Scope, error) {
	rows, err := s.db.Query(ctx, `SELECT `+scopeColumns+` FROM scopes WHERE org_id = $1 AND application_id = $2 ORDER BY name`, orgID, appID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []Scope{}
	for rows.Next() {
		sc, err := scanScope(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

// SetScopeStatus actualiza estado y, si currentDeploymentID no está vacío, el
// deployment actual. Es la versión sin condiciones, para el ciclo de undeploy
// (deleting, failed tras un undeploy fallido). Los deploys usan SetScopeDeployStatus.
func (s *Store) SetScopeStatus(ctx context.Context, orgID, id, status, currentDeploymentID string) error {
	_, err := s.db.Exec(ctx, `UPDATE scopes SET status = $3,
		current_deployment_id = CASE WHEN $4::text = '' THEN current_deployment_id ELSE $4::uuid END,
		updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, id, status, currentDeploymentID)
	return translate(err)
}

// SetScopeDeployStatus es SetScopeStatus para el ciclo de un deploy (deploying,
// healthy, failed). No pisa un scope en deleting: el undeploy encolado manda.
// Devuelve ErrConflict si el scope está en deleting (o no existe).
func (s *Store) SetScopeDeployStatus(ctx context.Context, orgID, id, status, currentDeploymentID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE scopes SET status = $3,
		current_deployment_id = CASE WHEN $4::text = '' THEN current_deployment_id ELSE $4::uuid END,
		updated_at = now() WHERE org_id = $1 AND id = $2 AND status <> $5`, orgID, id, status, currentDeploymentID, ScopeDeleting)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// ClearScopeDeployment deja el scope idle y sin deployment actual (tras un undeploy).
func (s *Store) ClearScopeDeployment(ctx context.Context, orgID, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE scopes SET status = $3, current_deployment_id = NULL, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, id, ScopeIdle)
	return translate(err)
}

// DeleteScope borra la fila del scope.
func (s *Store) DeleteScope(ctx context.Context, orgID, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM scopes WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
