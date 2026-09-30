-- ADR 0004: más nubes, credenciales cifradas, clusters de tipo vm, tokens de
-- bootstrap, secretos de aplicación y preferencia de idioma.

-- Providers nuevos (digitalocean, oci, ovh) y las credenciales de las nubes
-- con auth token, cifradas con AES-256-GCM (RAMPA_STATE_KEY) y el id de la
-- cuenta como dato autenticado. NULL: la cuenta no tiene credenciales guardadas.
ALTER TABLE cloud_accounts DROP CONSTRAINT cloud_accounts_provider_check;
ALTER TABLE cloud_accounts ADD CONSTRAINT cloud_accounts_provider_check
    CHECK (provider IN ('aws', 'gcp', 'azure', 'digitalocean', 'oci', 'ovh'));
ALTER TABLE cloud_accounts ADD COLUMN credentials_ciphertext bytea;

-- Un cluster es un Kubernetes administrado o registrado, o una máquina de un
-- solo nodo con k3s creada por Rampa.
ALTER TABLE clusters ADD COLUMN kind text NOT NULL DEFAULT 'kubernetes'
    CHECK (kind IN ('kubernetes', 'vm'));

-- Tokens de bootstrap: de un solo uso, vencen a las 2 horas, se canjean en
-- POST /v1/agent/bootstrap por el token real del cluster. Solo se guarda el hash.
CREATE TABLE cluster_bootstrap_tokens (
    id          uuid PRIMARY KEY,
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    cluster_id  uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cluster_bootstrap_tokens_cluster_idx ON cluster_bootstrap_tokens (cluster_id);

-- Claves de los secretos de aplicación que se escribieron por scope. Solo las
-- claves: los valores viajan al agente y no se persisten.
CREATE TABLE scope_secret_keys (
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    scope_id    uuid NOT NULL REFERENCES scopes(id) ON DELETE CASCADE,
    key         text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope_id, key)
);

-- Idioma preferido del usuario: '' (usa Accept-Language), 'es' o 'en'.
ALTER TABLE users ADD COLUMN locale text NOT NULL DEFAULT '';
