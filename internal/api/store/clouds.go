package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

const cloudAccountColumns = "id, org_id, provider, name, region, external_ref, settings, status, verification, (credentials_ciphertext IS NOT NULL), created_at, updated_at"

func scanCloudAccount(row interface{ Scan(dest ...any) error }) (*CloudAccount, error) {
	var a CloudAccount
	var settings, verification []byte
	if err := row.Scan(&a.ID, &a.OrgID, &a.Provider, &a.Name, &a.Region, &a.ExternalRef, &settings, &a.Status, &verification, &a.HasCredentials, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	a.Settings = map[string]string{}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &a.Settings)
	}
	if len(verification) > 0 && string(verification) != "null" {
		a.Verification = &Verification{}
		_ = json.Unmarshal(verification, a.Verification)
	}
	return &a, nil
}

// CreateCloudAccount crea la cuenta en estado pending.
func (s *Store) CreateCloudAccount(ctx context.Context, a *CloudAccount) (*CloudAccount, error) {
	if a.Settings == nil {
		a.Settings = map[string]string{}
	}
	settings, _ := json.Marshal(a.Settings)
	return scanCloudAccount(s.db.QueryRow(ctx, `INSERT INTO cloud_accounts (id, org_id, provider, name, region, external_ref, settings, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+cloudAccountColumns,
		uuid.NewString(), a.OrgID, a.Provider, a.Name, a.Region, a.ExternalRef, settings, CloudAccountPending))
}

// GetCloudAccount busca una cuenta de la org.
func (s *Store) GetCloudAccount(ctx context.Context, orgID, id string) (*CloudAccount, error) {
	return scanCloudAccount(s.db.QueryRow(ctx, `SELECT `+cloudAccountColumns+` FROM cloud_accounts WHERE org_id = $1 AND id = $2`, orgID, id))
}

// ListCloudAccounts lista las cuentas de la org.
func (s *Store) ListCloudAccounts(ctx context.Context, orgID string) ([]CloudAccount, error) {
	rows, err := s.db.Query(ctx, `SELECT `+cloudAccountColumns+` FROM cloud_accounts WHERE org_id = $1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []CloudAccount{}
	for rows.Next() {
		a, err := scanCloudAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// UpdateCloudAccountRef completa external_ref y settings. Vuelve a pending
// porque hay que verificar de nuevo.
func (s *Store) UpdateCloudAccountRef(ctx context.Context, orgID, id, externalRef string, settings map[string]string) (*CloudAccount, error) {
	b, _ := json.Marshal(settings)
	return scanCloudAccount(s.db.QueryRow(ctx, `UPDATE cloud_accounts SET external_ref = $3, settings = $4, status = $5, updated_at = now()
		WHERE org_id = $1 AND id = $2 RETURNING `+cloudAccountColumns, orgID, id, externalRef, b, CloudAccountPending))
}

// SetCloudAccountVerification guarda el resultado de Verify y el estado.
func (s *Store) SetCloudAccountVerification(ctx context.Context, orgID, id, status string, v Verification) (*CloudAccount, error) {
	b, _ := json.Marshal(v)
	return scanCloudAccount(s.db.QueryRow(ctx, `UPDATE cloud_accounts SET status = $3, verification = $4, updated_at = now()
		WHERE org_id = $1 AND id = $2 RETURNING `+cloudAccountColumns, orgID, id, status, b))
}

// SetCloudAccountCredentials guarda las credenciales cifradas de la cuenta (o
// las borra con nil). El cifrado es cosa del llamador (internal/api/crypto,
// con el id de la cuenta como dato autenticado). Vuelve a pending: hay que
// verificar de nuevo con las credenciales nuevas.
func (s *Store) SetCloudAccountCredentials(ctx context.Context, orgID, id string, ciphertext []byte) (*CloudAccount, error) {
	var ct any
	if len(ciphertext) > 0 {
		ct = ciphertext
	}
	return scanCloudAccount(s.db.QueryRow(ctx, `UPDATE cloud_accounts SET credentials_ciphertext = $3, status = $4, updated_at = now()
		WHERE org_id = $1 AND id = $2 RETURNING `+cloudAccountColumns, orgID, id, ct, CloudAccountPending))
}

// GetCloudAccountCredentials devuelve el cifrado de las credenciales, o nil si
// la cuenta no tiene. ErrNotFound si la cuenta no existe.
func (s *Store) GetCloudAccountCredentials(ctx context.Context, orgID, id string) ([]byte, error) {
	var ct []byte
	if err := s.db.QueryRow(ctx, `SELECT credentials_ciphertext FROM cloud_accounts WHERE org_id = $1 AND id = $2`, orgID, id).Scan(&ct); err != nil {
		return nil, translate(err)
	}
	return ct, nil
}

// DeleteCloudAccount borra la cuenta. Devuelve ErrConflict si tiene clusters.
func (s *Store) DeleteCloudAccount(ctx context.Context, orgID, id string) error {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM clusters WHERE org_id = $1 AND cloud_account_id = $2`, orgID, id).Scan(&n); err != nil {
		return translate(err)
	}
	if n > 0 {
		return ErrConflict
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM cloud_accounts WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- clusters ----

const clusterColumns = "id, org_id, cloud_account_id, name, kind, env, token_hash, agent_version, k8s_version, last_seen_at, status, domain, ingress_class, secret_store, agent_role_arn, infra_run_id, created_at, updated_at"

func scanCluster(row interface{ Scan(dest ...any) error }) (*Cluster, error) {
	var c Cluster
	var infraRunID *string
	if err := row.Scan(&c.ID, &c.OrgID, &c.CloudAccountID, &c.Name, &c.Kind, &c.Env, &c.TokenHash, &c.AgentVersion, &c.KubernetesVersion, &c.LastSeenAt, &c.Status, &c.Domain, &c.IngressClass, &c.SecretStore, &c.AgentRoleARN, &infraRunID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	c.InfraRunID = ptrString(infraRunID)
	return &c, nil
}

// CreateCluster crea el cluster con su token ya hasheado. Si c.ID viene
// seteado se respeta (el run de infraestructura lo necesita antes de insertar).
// Kind vacío es kubernetes.
func (s *Store) CreateCluster(ctx context.Context, c *Cluster) (*Cluster, error) {
	id := c.ID
	if id == "" {
		id = uuid.NewString()
	}
	kind := c.Kind
	if kind == "" {
		kind = ClusterKindKubernetes
	}
	return scanCluster(s.db.QueryRow(ctx, `INSERT INTO clusters (id, org_id, cloud_account_id, name, kind, env, token_hash, domain, ingress_class, secret_store, status, agent_role_arn, infra_run_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING `+clusterColumns,
		id, c.OrgID, c.CloudAccountID, c.Name, kind, c.Env, c.TokenHash, c.Domain, c.IngressClass, c.SecretStore, ClusterOffline, c.AgentRoleARN, nullString(c.InfraRunID)))
}

// SetClusterInfraRun enlaza el cluster con el run que lo crea.
func (s *Store) SetClusterInfraRun(ctx context.Context, orgID, id, runID string) error {
	_, err := s.db.Exec(ctx, `UPDATE clusters SET infra_run_id = $3, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, id, nullString(runID))
	return translate(err)
}

// SetClusterInfraResult guarda lo que dejó el run: el rol del agente (si no
// está vacío) y el estado (failed cuando el run falló, offline cuando terminó
// bien y el agente todavía no se conectó). Nunca pisa un cluster online.
func (s *Store) SetClusterInfraResult(ctx context.Context, orgID, id, agentRoleARN, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE clusters SET
		agent_role_arn = CASE WHEN $3 = '' THEN agent_role_arn ELSE $3 END,
		status = CASE WHEN status = $5 THEN status ELSE $4 END,
		updated_at = now()
		WHERE org_id = $1 AND id = $2`, orgID, id, agentRoleARN, status, ClusterOnline)
	return translate(err)
}

// GetCluster busca un cluster de la org.
func (s *Store) GetCluster(ctx context.Context, orgID, id string) (*Cluster, error) {
	return scanCluster(s.db.QueryRow(ctx, `SELECT `+clusterColumns+` FROM clusters WHERE org_id = $1 AND id = $2`, orgID, id))
}

// GetClusterByTokenHash busca el cluster por el hash de su token. Es global
// por diseño: es cómo el agente se autentica.
func (s *Store) GetClusterByTokenHash(ctx context.Context, hash string) (*Cluster, error) {
	return scanCluster(s.db.QueryRow(ctx, `SELECT `+clusterColumns+` FROM clusters WHERE token_hash = $1`, hash))
}

// ListClusters lista los clusters de la org. Si env no está vacío, filtra.
func (s *Store) ListClusters(ctx context.Context, orgID, env string) ([]Cluster, error) {
	rows, err := s.db.Query(ctx, `SELECT `+clusterColumns+` FROM clusters WHERE org_id = $1 AND ($2 = '' OR env = $2) ORDER BY created_at`, orgID, env)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []Cluster{}
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// RotateClusterToken reemplaza el hash del token.
func (s *Store) RotateClusterToken(ctx context.Context, orgID, id, newHash string) (*Cluster, error) {
	return scanCluster(s.db.QueryRow(ctx, `UPDATE clusters SET token_hash = $3, updated_at = now() WHERE org_id = $1 AND id = $2 RETURNING `+clusterColumns, orgID, id, newHash))
}

// SetClusterOnline marca el cluster online y actualiza versiones y last_seen.
func (s *Store) SetClusterOnline(ctx context.Context, orgID, id, agentVersion, k8sVersion string) error {
	_, err := s.db.Exec(ctx, `UPDATE clusters SET status = $3, last_seen_at = now(), updated_at = now(),
		agent_version = CASE WHEN $4 = '' THEN agent_version ELSE $4 END,
		k8s_version = CASE WHEN $5 = '' THEN k8s_version ELSE $5 END
		WHERE org_id = $1 AND id = $2`, orgID, id, ClusterOnline, agentVersion, k8sVersion)
	return translate(err)
}

// TouchCluster actualiza last_seen_at (heartbeat).
func (s *Store) TouchCluster(ctx context.Context, orgID, id, agentVersion, k8sVersion string) error {
	return s.SetClusterOnline(ctx, orgID, id, agentVersion, k8sVersion)
}

// SetClusterOffline marca el cluster offline.
func (s *Store) SetClusterOffline(ctx context.Context, orgID, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE clusters SET status = $3, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, id, ClusterOffline)
	return translate(err)
}

// MarkAllClustersOffline se usa al arrancar: ninguna conexión sobrevive un
// reinicio. Los clusters en failed (run de infraestructura fallido) se quedan así.
func (s *Store) MarkAllClustersOffline(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE clusters SET status = $1, updated_at = now() WHERE status = $2`, ClusterOffline, ClusterOnline)
	return translate(err)
}

// DeleteCluster borra el cluster. Devuelve ErrConflict si tiene scopes.
func (s *Store) DeleteCluster(ctx context.Context, orgID, id string) error {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM scopes WHERE org_id = $1 AND cluster_id = $2`, orgID, id).Scan(&n); err != nil {
		return translate(err)
	}
	if n > 0 {
		return ErrConflict
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM clusters WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
