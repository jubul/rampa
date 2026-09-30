package store

import (
	"context"
)

// UpsertScopeSecretKeys registra las claves de secretos escritas en un scope.
// Solo las claves: los valores nunca se persisten.
func (s *Store) UpsertScopeSecretKeys(ctx context.Context, orgID, scopeID string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `INSERT INTO scope_secret_keys (org_id, scope_id, key)
		SELECT $1, $2, k FROM unnest($3::text[]) AS k
		ON CONFLICT (scope_id, key) DO UPDATE SET updated_at = now()`, orgID, scopeID, keys)
	return translate(err)
}

// ListScopeSecretKeys devuelve las claves conocidas de un scope, ordenadas.
func (s *Store) ListScopeSecretKeys(ctx context.Context, orgID, scopeID string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT key FROM scope_secret_keys WHERE org_id = $1 AND scope_id = $2 ORDER BY key`, orgID, scopeID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
