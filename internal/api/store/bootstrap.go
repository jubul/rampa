package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const bootstrapColumns = "id, org_id, cluster_id, token_hash, expires_at, used_at, created_by, created_at"

func scanBootstrapToken(row interface{ Scan(dest ...any) error }) (*BootstrapToken, error) {
	var b BootstrapToken
	var createdBy *string
	if err := row.Scan(&b.ID, &b.OrgID, &b.ClusterID, &b.TokenHash, &b.ExpiresAt, &b.UsedAt, &createdBy, &b.CreatedAt); err != nil {
		return nil, translate(err)
	}
	b.CreatedBy = ptrString(createdBy)
	return &b, nil
}

// CreateBootstrapToken guarda el hash de un token de bootstrap del cluster.
// createdBy puede ser "" (lo generó el runner de infraestructura).
func (s *Store) CreateBootstrapToken(ctx context.Context, orgID, clusterID, hash string, expiresAt time.Time, createdBy string) (*BootstrapToken, error) {
	return scanBootstrapToken(s.db.QueryRow(ctx, `INSERT INTO cluster_bootstrap_tokens (id, org_id, cluster_id, token_hash, expires_at, created_by)
		SELECT $1, $2, $3, $4, $5, $6 FROM clusters WHERE org_id = $2 AND id = $3
		RETURNING `+bootstrapColumns, uuid.NewString(), orgID, clusterID, hash, expiresAt.UTC(), nullString(createdBy)))
}

// ConsumeBootstrapToken canjea un token por su hash: solo si existe, no se usó
// y no venció. Marca used_at en el mismo UPDATE, así dos canjes concurrentes
// no pueden ganar los dos. Es global por diseño: el hash es la credencial.
// Devuelve ErrNotFound si no hay un token canjeable con ese hash.
func (s *Store) ConsumeBootstrapToken(ctx context.Context, hash string) (*BootstrapToken, error) {
	return scanBootstrapToken(s.db.QueryRow(ctx, `UPDATE cluster_bootstrap_tokens SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING `+bootstrapColumns, hash))
}

// GetBootstrapTokenByHash busca un token por hash sin consumirlo (para
// distinguir en el log si un canje falló por vencido o usado).
func (s *Store) GetBootstrapTokenByHash(ctx context.Context, hash string) (*BootstrapToken, error) {
	return scanBootstrapToken(s.db.QueryRow(ctx, `SELECT `+bootstrapColumns+` FROM cluster_bootstrap_tokens WHERE token_hash = $1`, hash))
}

// ListBootstrapTokens lista los tokens de un cluster, del más nuevo al más viejo.
func (s *Store) ListBootstrapTokens(ctx context.Context, orgID, clusterID string) ([]BootstrapToken, error) {
	rows, err := s.db.Query(ctx, `SELECT `+bootstrapColumns+` FROM cluster_bootstrap_tokens WHERE org_id = $1 AND cluster_id = $2 ORDER BY created_at DESC`, orgID, clusterID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []BootstrapToken{}
	for rows.Next() {
		b, err := scanBootstrapToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// InvalidatePendingBootstrapTokens marca como usados los bootstrap tokens
// pendientes de un cluster (los que siguen sin canjear). Lo usa la rotación del
// token del cluster: un bootstrap emitido antes no puede volver a emitir un
// token después de que el admin rotó. Devuelve cuántos invalidó.
func (s *Store) InvalidatePendingBootstrapTokens(ctx context.Context, orgID, clusterID string) (int64, error) {
	tag, err := s.db.Exec(ctx, `UPDATE cluster_bootstrap_tokens SET used_at = now()
		WHERE org_id = $1 AND cluster_id = $2 AND used_at IS NULL`, orgID, clusterID)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}

// PurgeBootstrapTokens borra los tokens usados o vencidos hace más de keep.
func (s *Store) PurgeBootstrapTokens(ctx context.Context, keep time.Duration) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM cluster_bootstrap_tokens
		WHERE COALESCE(used_at, expires_at) < now() - make_interval(secs => $1)`, keep.Seconds())
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}
