-- Release F (KTD11). Durable backup-exception state machine. Four tables:
--   policies    — operator-declared freshness expectations (admin-managed)
--   exceptions  — the state machine; at most one OPEN row per condition identity
--   deliveries  — durable notification intents; at most one per (exception, transition)
--   lease       — collector work-suppression across replicas (NOT a correctness guard)
-- Style follows 000011/000013: IF NOT EXISTS, TEXT NOT NULL DEFAULT '', CHECK enums,
-- TIMESTAMPTZ, partial indexes, COMMENT ON TABLE.

CREATE TABLE IF NOT EXISTS backup_assurance_policies (
    id                UUID PRIMARY KEY,
    cluster_id        TEXT NOT NULL DEFAULT 'local',
    scope_kind        TEXT NOT NULL CHECK (scope_kind IN ('schedule', 'namespace', 'cluster')),
    scope_namespace   TEXT NOT NULL DEFAULT '',
    scope_name        TEXT NOT NULL DEFAULT '',
    max_age_seconds   INTEGER NOT NULL CHECK (max_age_seconds >= 300),
    grace_seconds     INTEGER NOT NULL DEFAULT 3600 CHECK (grace_seconds >= 0),
    treat_partial_as  TEXT NOT NULL DEFAULT 'failure' CHECK (treat_partial_as IN ('success', 'failure')),
    alert_on_paused   BOOLEAN NOT NULL DEFAULT TRUE,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_by        TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by        TEXT NOT NULL DEFAULT '',
    updated_at        TIMESTAMPTZ,
    revision          BIGINT NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_backup_assurance_policies_scope
    ON backup_assurance_policies (cluster_id, scope_kind, scope_namespace, scope_name);

CREATE TABLE IF NOT EXISTS backup_assurance_exceptions (
    id                 UUID PRIMARY KEY,
    cluster_id         TEXT NOT NULL DEFAULT 'local',
    policy_id          UUID NOT NULL REFERENCES backup_assurance_policies(id) ON DELETE CASCADE,
    subject_kind       TEXT NOT NULL CHECK (subject_kind IN ('schedule', 'namespace', 'cluster')),
    subject_namespace  TEXT NOT NULL DEFAULT '',
    subject_name       TEXT NOT NULL DEFAULT '',
    subject_uid        TEXT NOT NULL DEFAULT '',
    condition          TEXT NOT NULL CHECK (condition IN (
                           'overdue', 'failed', 'partially_failed', 'paused',
                           'never_run', 'location_unavailable', 'collection_unknown')),
    state              TEXT NOT NULL CHECK (state IN ('open', 'resolved')),
    severity           TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    opened_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_observed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at        TIMESTAMPTZ,
    observation_count  BIGINT NOT NULL DEFAULT 1,
    last_success_at    TIMESTAMPTZ,
    detail             JSONB NOT NULL DEFAULT '{}'
);

-- THE correctness guarantee. Mirrors 000014's partial-UNIQUE idiom: two replicas
-- cannot both open the same condition; the loser gets 23505 and downgrades to an
-- observation. Re-occurrence after resolution inserts a NEW row because the index
-- only covers state = 'open'.
CREATE UNIQUE INDEX IF NOT EXISTS idx_backup_assurance_exceptions_open_unique
    ON backup_assurance_exceptions
       (cluster_id, subject_kind, subject_namespace, subject_name, subject_uid, condition)
    WHERE state = 'open';

CREATE INDEX IF NOT EXISTS idx_backup_assurance_exceptions_read
    ON backup_assurance_exceptions (cluster_id, state, subject_namespace, opened_at DESC);

CREATE TABLE IF NOT EXISTS backup_assurance_deliveries (
    id            UUID PRIMARY KEY,
    exception_id  UUID NOT NULL REFERENCES backup_assurance_exceptions(id) ON DELETE CASCADE,
    transition    TEXT NOT NULL CHECK (transition IN ('opened', 'resolved')),
    state         TEXT NOT NULL CHECK (state IN ('pending', 'delivered', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at  TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT ''
);

-- One delivery intent per (exception, transition), forever. A restart between the
-- state commit and the notification leaves exactly one pending row for the next tick.
CREATE UNIQUE INDEX IF NOT EXISTS idx_backup_assurance_deliveries_once
    ON backup_assurance_deliveries (exception_id, transition);

CREATE INDEX IF NOT EXISTS idx_backup_assurance_deliveries_pending
    ON backup_assurance_deliveries (created_at)
    WHERE state = 'pending';

CREATE TABLE IF NOT EXISTS backup_assurance_collector_lease (
    cluster_id   TEXT PRIMARY KEY,
    holder       TEXT NOT NULL,
    acquired_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    renewed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL,
    fence        BIGINT NOT NULL DEFAULT 1
);

COMMENT ON TABLE backup_assurance_policies IS
    'Release F operator-declared backup freshness expectations. No rows means no evaluation — install does not imply a default policy.';
COMMENT ON TABLE backup_assurance_exceptions IS
    'Release F exception state machine (KTD11). Identity = (cluster_id, subject_kind, subject_namespace, subject_name, subject_uid, condition). At most one OPEN row per identity, enforced by a partial UNIQUE index (000014 precedent). detail JSONB is PRIVILEGED: it may carry controller messages and storage locations; filter before returning to a user.';
COMMENT ON TABLE backup_assurance_deliveries IS
    'Release F durable notification intents. UNIQUE (exception_id, transition) makes each state transition notifiable exactly once across process restarts; notifications.dedupWindow is a second layer against retried sends, not the primary guard.';
COMMENT ON TABLE backup_assurance_collector_lease IS
    'Release F collector work suppression. Advisory only: correctness comes from the exceptions/deliveries UNIQUE indexes. A lost or expired lease costs duplicate API reads, never a duplicate exception.';
