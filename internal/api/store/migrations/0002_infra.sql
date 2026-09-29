-- Infraestructura con clicks: runs de OpenTofu, su log y su estado cifrado.
-- Los clusters pueden nacer de un run (infra_run_id) y quedar en failed.

ALTER TABLE clusters ADD COLUMN agent_role_arn text NOT NULL DEFAULT '';
ALTER TABLE clusters ADD COLUMN infra_run_id uuid;
ALTER TABLE clusters DROP CONSTRAINT clusters_status_check;
ALTER TABLE clusters ADD CONSTRAINT clusters_status_check CHECK (status IN ('offline', 'online', 'failed'));

CREATE TABLE infra_runs (
    id                uuid PRIMARY KEY,
    org_id            uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind              text NOT NULL,
    provider          text NOT NULL,
    module            text NOT NULL,
    cloud_account_id  uuid NOT NULL REFERENCES cloud_accounts(id) ON DELETE RESTRICT,
    cluster_id        uuid REFERENCES clusters(id) ON DELETE SET NULL,
    name              text NOT NULL,
    operation         text NOT NULL DEFAULT 'apply' CHECK (operation IN ('apply', 'destroy')),
    status            text NOT NULL DEFAULT 'queued'
                      CHECK (status IN ('queued', 'initializing', 'planning', 'applying', 'succeeded', 'failed', 'destroying', 'destroyed')),
    -- inputs nunca contiene valores sensibles: el token del agente se inyecta al correr.
    inputs            jsonb NOT NULL DEFAULT '{}'::jsonb,
    outputs           jsonb NOT NULL DEFAULT '{}'::jsonb,
    error             text NOT NULL DEFAULT '',
    -- SHA-256 del token con el que el runner habla con el backend http de estado.
    state_token_hash  text NOT NULL UNIQUE,
    requested_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    attempt           int NOT NULL DEFAULT 0,
    -- locked_at lo pone el worker al tomar el run; queda NULL al terminar.
    locked_at         timestamptz,
    worker            text NOT NULL DEFAULT '',
    started_at        timestamptz,
    finished_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX infra_runs_org_created_idx ON infra_runs (org_id, created_at DESC);
CREATE INDEX infra_runs_pending_idx ON infra_runs (created_at) WHERE finished_at IS NULL;
CREATE INDEX infra_runs_cluster_idx ON infra_runs (cluster_id);

ALTER TABLE clusters
    ADD CONSTRAINT clusters_infra_run_fk
    FOREIGN KEY (infra_run_id) REFERENCES infra_runs(id) ON DELETE SET NULL;

CREATE TABLE infra_run_logs (
    run_id      uuid NOT NULL REFERENCES infra_runs(id) ON DELETE CASCADE,
    seq         int NOT NULL,
    line        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, seq)
);

CREATE TABLE infra_states (
    run_id      uuid PRIMARY KEY REFERENCES infra_runs(id) ON DELETE CASCADE,
    -- Estado de OpenTofu cifrado con AES-256-GCM (RAMPA_STATE_KEY). Vacío si solo hay lock.
    ciphertext  bytea NOT NULL DEFAULT ''::bytea,
    lock_info   jsonb,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
