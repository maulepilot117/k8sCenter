-- Personal saved views and resource pins (Release A; R5/R6, KTD3/KTD4).
--
-- owner_id is auth.User.ID verbatim: an opaque id for local users,
-- "oidc:<providerID>:<sub>" for OIDC, "ldap:<providerID>:<dn>" for LDAP.
-- It is deliberately NOT a foreign key to local_users — external identities
-- have no row there. Deleting a local user does not cascade; orphaned rows
-- are inert and are cleaned up by the operator, not by a cascade.
--
-- config is a validated, versioned JSONB envelope. The server rejects any
-- field outside the per-kind allowlist before the row is written, so the
-- database is the second line of defence, not the first.

CREATE TABLE IF NOT EXISTS user_preferences (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       TEXT        NOT NULL,
    kind           TEXT        NOT NULL CHECK (kind IN ('saved_view', 'pin')),
    name           TEXT        NOT NULL,
    cluster_id     TEXT        NOT NULL DEFAULT 'local',
    dedup_key      TEXT        NOT NULL,
    schema_version INTEGER     NOT NULL DEFAULT 1,
    revision       BIGINT      NOT NULL DEFAULT 1,
    config         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT user_preferences_owner_len   CHECK (char_length(owner_id)  BETWEEN 1 AND 512),
    CONSTRAINT user_preferences_name_len    CHECK (char_length(name)      BETWEEN 1 AND 128),
    CONSTRAINT user_preferences_cluster_len CHECK (char_length(cluster_id) BETWEEN 1 AND 64),
    CONSTRAINT user_preferences_dedup_len   CHECK (char_length(dedup_key) BETWEEN 1 AND 512),
    CONSTRAINT user_preferences_schema_ver  CHECK (schema_version >= 1),
    CONSTRAINT user_preferences_revision    CHECK (revision >= 1),
    CONSTRAINT user_preferences_config_size CHECK (pg_column_size(config) <= 8192)
);

-- Identity/dedup. For saved views dedup_key is lower(name); for pins it is
-- "<resourceKind>/<namespace>/<name>". UID is intentionally NOT part of the
-- key: a recreated same-name object must collide with the existing pin so it
-- can be reported as replaced rather than silently duplicating (R1, R6).
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_preferences_dedup
    ON user_preferences (owner_id, kind, cluster_id, dedup_key);

CREATE INDEX IF NOT EXISTS idx_user_preferences_owner_kind
    ON user_preferences (owner_id, kind, updated_at DESC);

COMMENT ON TABLE user_preferences IS
    'Per-user saved views and resource pins. Owner is auth.User.ID (provider-qualified). config is an allowlisted, schema-versioned JSONB envelope; revision drives optimistic concurrency. No cluster-derived content is stored — authorization is enforced when the referenced resource is fetched, not here.';
