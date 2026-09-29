package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// infraClaimLockID es el advisory lock con el que los workers serializan la
// toma de runs: así "un run por cuenta de nube" vale aunque haya varios
// procesos de la API.
const infraClaimLockID = 727263

const infraRunColumns = `id, org_id, kind, provider, module, cloud_account_id, cluster_id, name, operation, status, inputs, outputs, error,
	state_token_hash, requested_by, attempt, locked_at, started_at, finished_at, created_at, updated_at`

func scanInfraRun(row interface{ Scan(dest ...any) error }) (*InfraRun, error) {
	var r InfraRun
	var clusterID, requestedBy *string
	var inputs, outputs []byte
	if err := row.Scan(&r.ID, &r.OrgID, &r.Kind, &r.Provider, &r.Module, &r.CloudAccountID, &clusterID, &r.Name, &r.Operation, &r.Status, &inputs, &outputs, &r.Error,
		&r.StateTokenHash, &requestedBy, &r.Attempt, &r.LockedAt, &r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	r.ClusterID = ptrString(clusterID)
	r.RequestedBy = ptrString(requestedBy)
	r.Inputs = json.RawMessage(inputs)
	if len(r.Inputs) == 0 {
		r.Inputs = json.RawMessage("{}")
	}
	r.Outputs = json.RawMessage(outputs)
	if len(r.Outputs) == 0 {
		r.Outputs = json.RawMessage("{}")
	}
	return &r, nil
}

func (s *Store) queryInfraRuns(ctx context.Context, sql string, args ...any) ([]InfraRun, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []InfraRun{}
	for rows.Next() {
		r, err := scanInfraRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CreateInfraRun encola un run. Si r.ID viene seteado se respeta.
func (s *Store) CreateInfraRun(ctx context.Context, r *InfraRun) (*InfraRun, error) {
	id := r.ID
	if id == "" {
		id = uuid.NewString()
	}
	inputs := r.Inputs
	if len(inputs) == 0 {
		inputs = json.RawMessage("{}")
	}
	op := r.Operation
	if op == "" {
		op = InfraOpApply
	}
	return scanInfraRun(s.db.QueryRow(ctx, `INSERT INTO infra_runs (id, org_id, kind, provider, module, cloud_account_id, cluster_id, name, operation, status, inputs, state_token_hash, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING `+infraRunColumns,
		id, r.OrgID, r.Kind, r.Provider, r.Module, r.CloudAccountID, nullString(r.ClusterID), r.Name, op, InfraRunQueued, inputs, r.StateTokenHash, nullString(r.RequestedBy)))
}

// GetInfraRun busca un run de la org.
func (s *Store) GetInfraRun(ctx context.Context, orgID, id string) (*InfraRun, error) {
	return scanInfraRun(s.db.QueryRow(ctx, `SELECT `+infraRunColumns+` FROM infra_runs WHERE org_id = $1 AND id = $2`, orgID, id))
}

// GetInfraRunByStateTokenHash busca el run por el hash del token de su
// backend de estado. Es global por diseño, igual que los tokens: el hash es
// la credencial y el llamador verifica además que el id de la ruta coincida.
func (s *Store) GetInfraRunByStateTokenHash(ctx context.Context, hash string) (*InfraRun, error) {
	return scanInfraRun(s.db.QueryRow(ctx, `SELECT `+infraRunColumns+` FROM infra_runs WHERE state_token_hash = $1`, hash))
}

// ListInfraRuns lista los runs de la org del más nuevo al más viejo.
func (s *Store) ListInfraRuns(ctx context.Context, orgID string, limit int) ([]InfraRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	return s.queryInfraRuns(ctx, `SELECT `+infraRunColumns+` FROM infra_runs WHERE org_id = $1 ORDER BY created_at DESC LIMIT $2`, orgID, limit)
}

// ClaimInfraRun toma el run pendiente más viejo cuya cuenta de nube no tenga
// otro run en ejecución, lo marca con locked_at y worker y lo devuelve.
// ErrNotFound si no hay nada para hacer. Serializa con un advisory lock para
// que dos workers nunca tomen runs de la misma cuenta a la vez.
func (s *Store) ClaimInfraRun(ctx context.Context, worker string) (*InfraRun, error) {
	var run *InfraRun
	err := s.WithTx(ctx, func(tx *Store) error {
		if _, err := tx.db.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, infraClaimLockID); err != nil {
			return translate(err)
		}
		r, err := scanInfraRun(tx.db.QueryRow(ctx, `UPDATE infra_runs SET locked_at = now(), worker = $1, attempt = attempt + 1, updated_at = now()
			WHERE id = (
				SELECT r.id FROM infra_runs r
				WHERE r.finished_at IS NULL AND r.locked_at IS NULL AND r.status IN ($2, $3)
				  AND NOT EXISTS (
					SELECT 1 FROM infra_runs o
					WHERE o.cloud_account_id = r.cloud_account_id AND o.locked_at IS NOT NULL AND o.finished_at IS NULL
				  )
				ORDER BY r.created_at LIMIT 1
			) RETURNING `+infraRunColumns, worker, InfraRunQueued, InfraRunDestroying))
		if err != nil {
			return err
		}
		run = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return run, nil
}

// StartInfraRun registra el comienzo de una ejecución: started_at, estado
// inicial y el hash del token de estado nuevo de este intento.
func (s *Store) StartInfraRun(ctx context.Context, orgID, id, status, stateTokenHash string) (*InfraRun, error) {
	return scanInfraRun(s.db.QueryRow(ctx, `UPDATE infra_runs SET status = $3, state_token_hash = $4, started_at = now(), finished_at = NULL, error = '', updated_at = now()
		WHERE org_id = $1 AND id = $2 RETURNING `+infraRunColumns, orgID, id, status, stateTokenHash))
}

// SetInfraRunStatus cambia el estado de un run en curso.
func (s *Store) SetInfraRunStatus(ctx context.Context, orgID, id, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE infra_runs SET status = $3, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, id, status)
	return translate(err)
}

// FinishInfraRun cierra el run con un estado terminal, el error (si hubo) y
// los outputs (si vienen). Libera el lock del worker.
func (s *Store) FinishInfraRun(ctx context.Context, orgID, id, status, errMsg string, outputs json.RawMessage) (*InfraRun, error) {
	var out any
	if len(outputs) > 0 {
		out = outputs
	}
	return scanInfraRun(s.db.QueryRow(ctx, `UPDATE infra_runs SET status = $3, error = $4, outputs = COALESCE($5, outputs),
		finished_at = now(), locked_at = NULL, updated_at = now()
		WHERE org_id = $1 AND id = $2 RETURNING `+infraRunColumns, orgID, id, status, errMsg, out))
}

// RequeueInfraRun vuelve a encolar un run terminado con otra operación (o la
// misma): retry de un failed, o destroy de un succeeded o failed. Solo toca
// runs que no están corriendo; si el run está en curso devuelve ErrConflict.
func (s *Store) RequeueInfraRun(ctx context.Context, orgID, id, operation, status string, allowedFrom []string) (*InfraRun, error) {
	tag, err := s.db.Exec(ctx, `UPDATE infra_runs SET operation = $3, status = $4, error = '', locked_at = NULL, started_at = NULL, finished_at = NULL, updated_at = now()
		WHERE org_id = $1 AND id = $2 AND locked_at IS NULL AND status = ANY($5::text[])`, orgID, id, operation, status, allowedFrom)
	if err != nil {
		return nil, translate(err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetInfraRun(ctx, orgID, id); err != nil {
			return nil, err
		}
		return nil, ErrConflict
	}
	return s.GetInfraRun(ctx, orgID, id)
}

// FailOrphanInfraRuns cierra como failed los runs que un proceso anterior
// dejó tomados (locked_at) sin terminar: la API se reinició en el medio.
// Devuelve los runs afectados para que el llamador actualice sus clusters.
func (s *Store) FailOrphanInfraRuns(ctx context.Context, errMsg string) ([]InfraRun, error) {
	return s.queryInfraRuns(ctx, `UPDATE infra_runs SET status = $1, error = $2, finished_at = now(), locked_at = NULL, updated_at = now()
		WHERE locked_at IS NOT NULL AND finished_at IS NULL RETURNING `+infraRunColumns, InfraRunFailed, errMsg)
}

// CountRunningInfraRuns cuenta los runs tomados por algún worker (para tests y métricas).
func (s *Store) CountRunningInfraRuns(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM infra_runs WHERE locked_at IS NOT NULL AND finished_at IS NULL`).Scan(&n)
	return n, translate(err)
}

// ---- log ----

// AppendInfraRunLog agrega una línea con su seq. El org_id se verifica por el run.
func (s *Store) AppendInfraRunLog(ctx context.Context, orgID, runID string, seq int, line string) error {
	tag, err := s.db.Exec(ctx, `INSERT INTO infra_run_logs (run_id, seq, line)
		SELECT id, $3, $4 FROM infra_runs WHERE org_id = $1 AND id = $2`, orgID, runID, seq, line)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MaxInfraRunLogSeq devuelve el último seq del log de un run (0 si está vacío).
func (s *Store) MaxInfraRunLogSeq(ctx context.Context, orgID, runID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT COALESCE(max(l.seq), 0) FROM infra_run_logs l JOIN infra_runs r ON r.id = l.run_id
		WHERE r.org_id = $1 AND l.run_id = $2`, orgID, runID).Scan(&n)
	return n, translate(err)
}

// ListInfraRunLog devuelve las líneas con seq > afterSeq, en orden.
func (s *Store) ListInfraRunLog(ctx context.Context, orgID, runID string, afterSeq int) ([]InfraRunLog, error) {
	rows, err := s.db.Query(ctx, `SELECT l.run_id, l.seq, l.line, l.created_at FROM infra_run_logs l JOIN infra_runs r ON r.id = l.run_id
		WHERE r.org_id = $1 AND l.run_id = $2 AND l.seq > $3 ORDER BY l.seq`, orgID, runID, afterSeq)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []InfraRunLog{}
	for rows.Next() {
		var l InfraRunLog
		if err := rows.Scan(&l.RunID, &l.Seq, &l.Line, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// InfraRunLogTail devuelve las últimas n líneas, en orden.
func (s *Store) InfraRunLogTail(ctx context.Context, orgID, runID string, n int) ([]string, error) {
	if n <= 0 {
		n = 20
	}
	rows, err := s.db.Query(ctx, `SELECT line FROM (
			SELECT l.seq, l.line FROM infra_run_logs l JOIN infra_runs r ON r.id = l.run_id
			WHERE r.org_id = $1 AND l.run_id = $2 ORDER BY l.seq DESC LIMIT $3
		) t ORDER BY t.seq`, orgID, runID, n)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		out = append(out, line)
	}
	return out, rows.Err()
}

// ---- estado de OpenTofu ----

// GetInfraState devuelve el estado cifrado y el lock de un run. ErrNotFound si
// nunca se guardó nada (ni estado ni lock).
func (s *Store) GetInfraState(ctx context.Context, runID string) (*InfraState, error) {
	var st InfraState
	var lock []byte
	if err := s.db.QueryRow(ctx, `SELECT run_id, ciphertext, lock_info, updated_at FROM infra_states WHERE run_id = $1`, runID).
		Scan(&st.RunID, &st.Ciphertext, &lock, &st.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	if len(lock) > 0 && string(lock) != "null" {
		st.LockInfo = json.RawMessage(lock)
	}
	return &st, nil
}

// PutInfraState guarda el estado cifrado (conserva el lock).
func (s *Store) PutInfraState(ctx context.Context, runID string, ciphertext []byte) error {
	_, err := s.db.Exec(ctx, `INSERT INTO infra_states (run_id, ciphertext) VALUES ($1, $2)
		ON CONFLICT (run_id) DO UPDATE SET ciphertext = EXCLUDED.ciphertext, updated_at = now()`, runID, ciphertext)
	return translate(err)
}

// DeleteInfraState borra el estado (deja el lock si lo hay).
func (s *Store) DeleteInfraState(ctx context.Context, runID string) error {
	_, err := s.db.Exec(ctx, `UPDATE infra_states SET ciphertext = ''::bytea, updated_at = now() WHERE run_id = $1`, runID)
	return translate(err)
}

// LockInfraState intenta tomar el lock con lockInfo. Devuelve (true, nil) si
// lo tomó; (false, actual) si ya estaba tomado, con el lock vigente. Es
// atómico: un solo UPDATE condicional.
func (s *Store) LockInfraState(ctx context.Context, runID string, lockInfo json.RawMessage) (bool, json.RawMessage, error) {
	var acquired bool
	var current json.RawMessage
	err := s.WithTx(ctx, func(tx *Store) error {
		tag, err := tx.db.Exec(ctx, `INSERT INTO infra_states (run_id, lock_info) VALUES ($1, $2)
			ON CONFLICT (run_id) DO UPDATE SET lock_info = EXCLUDED.lock_info, updated_at = now()
			WHERE infra_states.lock_info IS NULL`, runID, lockInfo)
		if err != nil {
			return translate(err)
		}
		if tag.RowsAffected() > 0 {
			acquired = true
			return nil
		}
		var lock []byte
		if err := tx.db.QueryRow(ctx, `SELECT lock_info FROM infra_states WHERE run_id = $1`, runID).Scan(&lock); err != nil {
			return translate(err)
		}
		current = json.RawMessage(lock)
		return nil
	})
	if err != nil {
		return false, nil, err
	}
	return acquired, current, nil
}

// UnlockInfraState libera el lock. Es idempotente.
func (s *Store) UnlockInfraState(ctx context.Context, runID string) error {
	_, err := s.db.Exec(ctx, `UPDATE infra_states SET lock_info = NULL, updated_at = now() WHERE run_id = $1`, runID)
	return translate(err)
}
