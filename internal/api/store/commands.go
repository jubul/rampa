package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const commandColumns = "id, org_id, cluster_id, kind, payload, status, result, deployment_id, scope_id, sent_at, acked_at, finished_at, created_at, updated_at"

func scanCommand(row interface{ Scan(dest ...any) error }) (*AgentCommand, error) {
	var c AgentCommand
	var payload, result []byte
	var deploymentID, scopeID *string
	if err := row.Scan(&c.ID, &c.OrgID, &c.ClusterID, &c.Kind, &payload, &c.Status, &result, &deploymentID, &scopeID, &c.SentAt, &c.AckedAt, &c.FinishedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	c.Payload = json.RawMessage(payload)
	if len(result) > 0 && string(result) != "null" {
		c.Result = json.RawMessage(result)
	}
	c.DeploymentID = ptrString(deploymentID)
	c.ScopeID = ptrString(scopeID)
	return &c, nil
}

// CreateAgentCommand encola un comando. El id lo trae el llamador porque es
// también el id dentro del payload (protocol.Command.ID).
func (s *Store) CreateAgentCommand(ctx context.Context, c *AgentCommand) (*AgentCommand, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return scanCommand(s.db.QueryRow(ctx, `INSERT INTO agent_commands (id, org_id, cluster_id, kind, payload, status, deployment_id, scope_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+commandColumns,
		c.ID, c.OrgID, c.ClusterID, c.Kind, c.Payload, CommandQueued, nullString(c.DeploymentID), nullString(c.ScopeID)))
}

// GetAgentCommand busca un comando. Filtra por org y cluster: un agente solo ve los suyos.
func (s *Store) GetAgentCommand(ctx context.Context, orgID, clusterID, id string) (*AgentCommand, error) {
	return scanCommand(s.db.QueryRow(ctx, `SELECT `+commandColumns+` FROM agent_commands WHERE org_id = $1 AND cluster_id = $2 AND id = $3`, orgID, clusterID, id))
}

// ListAgentCommandsForDeployment lista los comandos de un deployment.
func (s *Store) ListAgentCommandsForDeployment(ctx context.Context, orgID, deploymentID string) ([]AgentCommand, error) {
	rows, err := s.db.Query(ctx, `SELECT `+commandColumns+` FROM agent_commands WHERE org_id = $1 AND deployment_id = $2 ORDER BY created_at`, orgID, deploymentID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []AgentCommand{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// LiveOnlyKinds son los comandos que se entregan solo por la conexión viva y
// nunca se reenvían desde la cola: su payload persistido no está completo
// (write_secret se guarda sin los valores). Si el agente no hace ack, el
// reaper los cierra como failed.
var LiveOnlyKinds = []string{"write_secret"}

// PendingAgentCommands devuelve los comandos que hay que (re)enviar a un
// cluster: los queued y, si includeSent, los sent que nunca recibieron ack.
// Excluye LiveOnlyKinds.
func (s *Store) PendingAgentCommands(ctx context.Context, orgID, clusterID string, includeSent bool) ([]AgentCommand, error) {
	rows, err := s.db.Query(ctx, `SELECT `+commandColumns+` FROM agent_commands
		WHERE org_id = $1 AND cluster_id = $2 AND (status = $3 OR ($4 AND status = $5 AND acked_at IS NULL))
		  AND kind <> ALL($6::text[])
		ORDER BY created_at`, orgID, clusterID, CommandQueued, includeSent, CommandSent, LiveOnlyKinds)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []AgentCommand{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// CountPendingAgentCommands cuenta queued más sent sin ack, sin LiveOnlyKinds.
func (s *Store) CountPendingAgentCommands(ctx context.Context, orgID, clusterID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM agent_commands WHERE org_id = $1 AND cluster_id = $2 AND (status = $3 OR (status = $4 AND acked_at IS NULL)) AND kind <> ALL($5::text[])`,
		orgID, clusterID, CommandQueued, CommandSent, LiveOnlyKinds).Scan(&n)
	return n, translate(err)
}

// MarkAgentCommandSent pasa un comando a sent (solo si estaba queued o sent).
func (s *Store) MarkAgentCommandSent(ctx context.Context, orgID, clusterID, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE agent_commands SET status = $4, sent_at = now(), updated_at = now()
		WHERE org_id = $1 AND cluster_id = $2 AND id = $3 AND status IN ($5, $4)`, orgID, clusterID, id, CommandSent, CommandQueued)
	return translate(err)
}

// MarkAgentCommandAcked registra el ack. Devuelve ErrNotFound si el comando no es de ese cluster.
func (s *Store) MarkAgentCommandAcked(ctx context.Context, orgID, clusterID, id string) error {
	tag, err := s.db.Exec(ctx, `UPDATE agent_commands SET status = $4, acked_at = COALESCE(acked_at, now()), updated_at = now()
		WHERE org_id = $1 AND cluster_id = $2 AND id = $3 AND status IN ($5, $6, $4)`, orgID, clusterID, id, CommandAcked, CommandQueued, CommandSent)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FinishAgentCommand cierra un comando con done o failed y guarda el result.
// Solo cierra comandos que todavía no terminaron: si ya estaba done o failed
// (un result repetido, o el reaper y el agente a la vez) devuelve ErrNotFound
// y no pisa nada.
func (s *Store) FinishAgentCommand(ctx context.Context, orgID, clusterID, id, status string, result json.RawMessage) error {
	var res any
	if len(result) > 0 {
		res = result
	}
	tag, err := s.db.Exec(ctx, `UPDATE agent_commands SET status = $4, result = COALESCE($5, result), finished_at = now(), acked_at = COALESCE(acked_at, now()), updated_at = now()
		WHERE org_id = $1 AND cluster_id = $2 AND id = $3 AND status IN ($6, $7, $8)`,
		orgID, clusterID, id, status, res, CommandQueued, CommandSent, CommandAcked)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FailQueuedCommandsForDeployment marca como failed los comandos de un
// deployment que el agente todavía no tomó: queued, o sent sin ack. Devuelve
// cuántos afectó; si es 0 y el deployment tenía comando, el agente ya lo tomó.
func (s *Store) FailQueuedCommandsForDeployment(ctx context.Context, orgID, deploymentID, message string) (int64, error) {
	result, _ := json.Marshal(map[string]string{"status": "cancelled", "message": message})
	tag, err := s.db.Exec(ctx, `UPDATE agent_commands SET status = $3, result = $4, finished_at = now(), updated_at = now()
		WHERE org_id = $1 AND deployment_id = $2 AND (status = $5 OR (status = $6 AND acked_at IS NULL))`,
		orgID, deploymentID, CommandFailed, result, CommandQueued, CommandSent)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}

// CountAckedCommandsForDeployment cuenta los comandos del deployment que el
// agente ya reconoció y todavía no terminó.
func (s *Store) CountAckedCommandsForDeployment(ctx context.Context, orgID, deploymentID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM agent_commands WHERE org_id = $1 AND deployment_id = $2 AND status = $3`,
		orgID, deploymentID, CommandAcked).Scan(&n)
	return n, translate(err)
}

// AckedAgentCommands devuelve los comandos de un cluster que el agente
// reconoció y no cerró. Al reconectar, los que el agente no declare en curso
// perdieron su result.
func (s *Store) AckedAgentCommands(ctx context.Context, orgID, clusterID string) ([]AgentCommand, error) {
	rows, err := s.db.Query(ctx, `SELECT `+commandColumns+` FROM agent_commands
		WHERE org_id = $1 AND cluster_id = $2 AND status = $3 ORDER BY created_at`, orgID, clusterID, CommandAcked)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []AgentCommand{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// StaleAgentCommands devuelve, de todas las orgs, los comandos sent o acked
// cuya antigüedad (desde el ack, o desde el envío) supera el timeout_seconds
// del payload más grace. Los comandos sin timeout en el payload (undeploy)
// usan defaultTimeoutSeconds. Es global a propósito: lo consume el reaper
// periódico, que después filtra cada fila por su org y cluster.
func (s *Store) StaleAgentCommands(ctx context.Context, defaultTimeoutSeconds int, grace time.Duration, limit int) ([]AgentCommand, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `SELECT `+commandColumns+` FROM agent_commands
		WHERE status IN ($1, $2)
		  AND COALESCE(acked_at, sent_at, created_at)
		      + make_interval(secs => COALESCE(NULLIF(payload->'deploy'->>'timeout_seconds', '')::int, $3))
		      + make_interval(secs => $4) < now()
		ORDER BY created_at LIMIT $5`,
		CommandSent, CommandAcked, defaultTimeoutSeconds, grace.Seconds(), limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []AgentCommand{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}
