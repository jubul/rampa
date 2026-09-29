-- Esquema inicial del plano de control de Rampa (Fase A).
-- Todas las tablas cuelgan de organizations y llevan org_id, created_at y updated_at.
-- Los ids los genera la aplicación (UUID v4).

CREATE TABLE organizations (
    id          uuid PRIMARY KEY,
    slug        text NOT NULL UNIQUE,
    name        text NOT NULL,
    plan        text NOT NULL DEFAULT 'free',
    settings    jsonb NOT NULL DEFAULT '{"require_approval_prod": true}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id          uuid PRIMARY KEY,
    email       text NOT NULL UNIQUE,
    name        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        text NOT NULL CHECK (role IN ('owner', 'admin', 'developer', 'viewer')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX memberships_user_idx ON memberships (user_id);

CREATE TABLE api_tokens (
    id            uuid PRIMARY KEY,
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          text NOT NULL,
    hash          text NOT NULL UNIQUE,
    role          text NOT NULL CHECK (role IN ('owner', 'admin', 'developer', 'viewer')),
    expires_at    timestamptz,
    last_used_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_tokens_org_idx ON api_tokens (org_id);

CREATE TABLE cloud_accounts (
    id            uuid PRIMARY KEY,
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider      text NOT NULL CHECK (provider IN ('aws', 'gcp', 'azure')),
    name          text NOT NULL,
    region        text NOT NULL DEFAULT '',
    external_ref  text NOT NULL DEFAULT '',
    settings      jsonb NOT NULL DEFAULT '{}'::jsonb,
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'connected', 'failed')),
    verification  jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE clusters (
    id                uuid PRIMARY KEY,
    org_id            uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    cloud_account_id  uuid NOT NULL REFERENCES cloud_accounts(id) ON DELETE RESTRICT,
    name              text NOT NULL,
    env               text NOT NULL CHECK (env IN ('nonprod', 'prod')),
    token_hash        text NOT NULL UNIQUE,
    agent_version     text NOT NULL DEFAULT '',
    k8s_version       text NOT NULL DEFAULT '',
    last_seen_at      timestamptz,
    status            text NOT NULL DEFAULT 'offline' CHECK (status IN ('offline', 'online')),
    domain            text NOT NULL DEFAULT '',
    ingress_class     text NOT NULL DEFAULT '',
    secret_store      text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);
CREATE INDEX clusters_org_env_idx ON clusters (org_id, env);
CREATE INDEX clusters_cloud_account_idx ON clusters (cloud_account_id);

CREATE TABLE applications (
    id            uuid PRIMARY KEY,
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name          text NOT NULL,
    team          text NOT NULL,
    tier          int NOT NULL DEFAULT 2,
    description   text NOT NULL DEFAULT '',
    type          text NOT NULL,
    runtime       text NOT NULL DEFAULT '',
    repo_url      text NOT NULL DEFAULT '',
    manifest      text NOT NULL,
    manifest_sha  text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE scopes (
    id                     uuid PRIMARY KEY,
    org_id                 uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    application_id         uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    name                   text NOT NULL,
    cluster_id             uuid REFERENCES clusters(id) ON DELETE RESTRICT,
    ephemeral              boolean NOT NULL DEFAULT false,
    status                 text NOT NULL DEFAULT 'unbound',
    current_deployment_id  uuid,
    ttl_expires_at         timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, application_id, name)
);
CREATE INDEX scopes_cluster_idx ON scopes (cluster_id);

CREATE TABLE deployments (
    id              uuid PRIMARY KEY,
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    application_id  uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    scope_id        uuid NOT NULL REFERENCES scopes(id) ON DELETE CASCADE,
    cluster_id      uuid NOT NULL REFERENCES clusters(id) ON DELETE RESTRICT,
    image           text NOT NULL,
    values          jsonb NOT NULL,
    status          text NOT NULL,
    message         text NOT NULL DEFAULT '',
    requested_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    approved_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    approval_id     uuid,
    workload        jsonb,
    started_at      timestamptz,
    finished_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX deployments_scope_created_idx ON deployments (org_id, scope_id, created_at DESC);
CREATE INDEX deployments_status_idx ON deployments (org_id, status);

ALTER TABLE scopes
    ADD CONSTRAINT scopes_current_deployment_fk
    FOREIGN KEY (current_deployment_id) REFERENCES deployments(id) ON DELETE SET NULL;

CREATE TABLE approvals (
    id                  uuid PRIMARY KEY,
    org_id              uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    deployment_id       uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    required_by_policy  boolean NOT NULL DEFAULT true,
    approved_by         uuid REFERENCES users(id) ON DELETE SET NULL,
    approved_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approvals_deployment_idx ON approvals (deployment_id);

CREATE TABLE parameters (
    id              uuid PRIMARY KEY,
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    application_id  uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    scope           text NOT NULL DEFAULT '',
    name            text NOT NULL,
    value           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, application_id, scope, name)
);

CREATE TABLE services (
    id                  uuid PRIMARY KEY,
    org_id              uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    application_id      uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    name                text NOT NULL,
    type                text NOT NULL,
    size                text NOT NULL DEFAULT 'S',
    scopes              text[] NOT NULL DEFAULT '{}',
    status              text NOT NULL DEFAULT 'pending',
    provider_state_ref  text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, application_id, name)
);

CREATE TABLE events (
    id            uuid PRIMARY KEY,
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    actor_type    text NOT NULL,
    actor_id      text NOT NULL DEFAULT '',
    actor_name    text NOT NULL DEFAULT '',
    kind          text NOT NULL,
    subject_type  text NOT NULL,
    subject_id    text NOT NULL DEFAULT '',
    message       text NOT NULL DEFAULT '',
    payload       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_org_created_idx ON events (org_id, created_at DESC);
CREATE INDEX events_subject_idx ON events (org_id, subject_type, subject_id, created_at DESC);

CREATE TABLE agent_commands (
    id             uuid PRIMARY KEY,
    org_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    cluster_id     uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    kind           text NOT NULL,
    payload        jsonb NOT NULL,
    status         text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'acked', 'done', 'failed')),
    result         jsonb,
    deployment_id  uuid REFERENCES deployments(id) ON DELETE CASCADE,
    scope_id       uuid REFERENCES scopes(id) ON DELETE SET NULL,
    sent_at        timestamptz,
    acked_at       timestamptz,
    finished_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_commands_pending_idx ON agent_commands (cluster_id, status, created_at)
    WHERE status IN ('queued', 'sent');
CREATE INDEX agent_commands_deployment_idx ON agent_commands (deployment_id);
