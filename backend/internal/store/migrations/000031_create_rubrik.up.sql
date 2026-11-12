-- Rubrik backup provider. Migration 000031. Creates every table the Rubrik
-- Security Cloud (RSC) integration uses and adds 'rubrik' to the notification
-- source CHECK. Purely additive apart from that constraint: no backfill.
-- Re-runnable (IF NOT EXISTS, DROP CONSTRAINT IF EXISTS, COMMENT ON); see
-- NOTES.txt (000031).
--
-- Tables:
--   rubrik_connection           the RSC tenant connection (one row in production)
--   rubrik_cluster_mappings     k8sCenter cluster <-> RSC Kubernetes cluster
--   rubrik_operations           durable records of snapshot, SLA and restore writes
--   rubrik_poller_state         per mapped cluster: activity cursor and connection streak
--   rubrik_compliance_states    per RSC object: the poller's SLA compliance judgment
--   rubrik_poller_events        notification-worthy transitions, one row per transition
--   rubrik_poller_deliveries    durable notification intents, one per event
--   rubrik_poller_lease         poller work suppression across replicas

CREATE TABLE IF NOT EXISTS rubrik_connection (
    id                 TEXT PRIMARY KEY,
    tenant_url         TEXT NOT NULL,
    client_id          TEXT NOT NULL,
    client_secret      BYTEA NOT NULL,
    secret_origin      TEXT NOT NULL CHECK (secret_origin <> ''),
    revision           BIGINT NOT NULL DEFAULT 1,
    updated_by         TEXT NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_success_at    TIMESTAMPTZ,
    last_failure_at    TIMESTAMPTZ,
    last_failure_kind  TEXT NOT NULL DEFAULT '' CHECK (last_failure_kind IN (
                           '', 'auth_failed', 'unreachable', 'rate_limited',
                           'graphql_error', 'unreadable'))
);

CREATE TABLE IF NOT EXISTS rubrik_cluster_mappings (
    cluster_id          TEXT PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    connection_id       TEXT NOT NULL REFERENCES rubrik_connection(id) ON DELETE CASCADE,
    rsc_cluster_id      TEXT NOT NULL CHECK (rsc_cluster_id <> ''),
    rsc_cluster_name    TEXT NOT NULL DEFAULT '',
    cluster_generation  TEXT NOT NULL CHECK (cluster_generation <> ''),
    created_by          TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by          TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Each RSC Kubernetes cluster maps to at most one k8sCenter cluster. The
-- other direction is the primary key.
CREATE UNIQUE INDEX IF NOT EXISTS idx_rubrik_cluster_mappings_rsc
    ON rubrik_cluster_mappings (connection_id, rsc_cluster_id);

CREATE TABLE IF NOT EXISTS rubrik_operations (
    id               UUID PRIMARY KEY,
    owner            TEXT NOT NULL,
    cluster_id       TEXT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('snapshot', 'sla_assign', 'restore')),
    namespace        TEXT NOT NULL,
    rsc_object_id    TEXT NOT NULL,
    request_hash     TEXT NOT NULL,
    request          JSONB NOT NULL DEFAULT '{}',
    source           JSONB NOT NULL DEFAULT '{}',
    state            TEXT NOT NULL CHECK (state IN (
                         'applying', 'running', 'succeeded', 'failed', 'partial', 'unknown')),
    job_family       TEXT NOT NULL DEFAULT '' CHECK (job_family IN ('', 'taskchain', 'async_request')),
    job_handle       TEXT NOT NULL DEFAULT '',
    failure_message  TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at      TIMESTAMPTZ
);

-- One in-flight restore or SLA change per RSC object, across replicas. An
-- 'unknown' outcome keeps blocking until a refresh settles it.
CREATE UNIQUE INDEX IF NOT EXISTS idx_rubrik_operations_in_flight
    ON rubrik_operations (cluster_id, rsc_object_id)
    WHERE kind IN ('sla_assign', 'restore') AND state IN ('applying', 'running', 'unknown');

CREATE INDEX IF NOT EXISTS idx_rubrik_operations_open
    ON rubrik_operations (cluster_id, updated_at)
    WHERE state IN ('applying', 'running', 'unknown');

CREATE INDEX IF NOT EXISTS idx_rubrik_operations_owner
    ON rubrik_operations (owner, created_at DESC);

CREATE TABLE IF NOT EXISTS rubrik_poller_state (
    cluster_id                 TEXT PRIMARY KEY REFERENCES rubrik_cluster_mappings(cluster_id) ON DELETE CASCADE,
    activity_cursor            TIMESTAMPTZ,
    connection_failure_streak  INTEGER NOT NULL DEFAULT 0 CHECK (connection_failure_streak >= 0),
    connection_failing         BOOLEAN NOT NULL DEFAULT FALSE,
    last_tick_at               TIMESTAMPTZ,
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rubrik_compliance_states (
    cluster_id     TEXT NOT NULL REFERENCES rubrik_cluster_mappings(cluster_id) ON DELETE CASCADE,
    rsc_object_id  TEXT NOT NULL,
    namespace      TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL CHECK (status IN (
                       'in_compliance', 'late', 'out_of_compliance', 'unknown')),
    late_streak    INTEGER NOT NULL DEFAULT 0 CHECK (late_streak >= 0),
    changed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    evaluated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (cluster_id, rsc_object_id)
);

CREATE TABLE IF NOT EXISTS rubrik_poller_events (
    id             UUID PRIMARY KEY,
    cluster_id     TEXT NOT NULL REFERENCES rubrik_cluster_mappings(cluster_id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN (
                       'snapshot_failed', 'compliance_lost', 'compliance_restored',
                       'connection_failing', 'connection_restored', 'monitoring_gap')),
    rsc_object_id  TEXT NOT NULL DEFAULT '',
    namespace      TEXT NOT NULL DEFAULT '',
    dedup_key      TEXT NOT NULL CHECK (dedup_key <> ''),
    occurred_at    TIMESTAMPTZ NOT NULL,
    detail         JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One event per transition, however many replicas observe it.
CREATE UNIQUE INDEX IF NOT EXISTS idx_rubrik_poller_events_once
    ON rubrik_poller_events (cluster_id, kind, dedup_key);

CREATE TABLE IF NOT EXISTS rubrik_poller_deliveries (
    id            UUID PRIMARY KEY,
    event_id      UUID NOT NULL UNIQUE REFERENCES rubrik_poller_events(id) ON DELETE CASCADE,
    state         TEXT NOT NULL CHECK (state IN ('pending', 'delivered', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at  TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_rubrik_poller_deliveries_pending
    ON rubrik_poller_deliveries (created_at)
    WHERE state = 'pending';

CREATE TABLE IF NOT EXISTS rubrik_poller_lease (
    cluster_id   TEXT PRIMARY KEY,
    holder       TEXT NOT NULL,
    acquired_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    renewed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL,
    fence        BIGINT NOT NULL DEFAULT 1
);

ALTER TABLE nc_notifications DROP CONSTRAINT IF EXISTS nc_notifications_source_check;
ALTER TABLE nc_notifications ADD CONSTRAINT nc_notifications_source_check
    CHECK (source IN (
        'alert',
        'policy',
        'gitops',
        'diagnostic',
        'scan',
        'cluster',
        'audit',
        'velero',
        'certmanager',
        'limits',
        'external_secrets',
        'rubrik'
    ));

COMMENT ON TABLE rubrik_connection IS
    'RSC tenant connection. client_secret is AES-256-GCM encrypted with the credentials master secret (store.Encrypt) and is never returned by the API, logged or audited. It is usable only while secret_origin equals the origin of tenant_url. revision advances on every save so every replica notices. Production uses id = default. See NOTES.txt (000031).';
COMMENT ON TABLE rubrik_cluster_mappings IS
    'k8sCenter cluster to RSC Kubernetes cluster, one to one in both directions. cluster_generation is the cluster registration generation at mapping time (created_at RFC3339Nano, or the literal local); a mismatch makes the mapping invalid. Deleted with the cluster, and with every poller row through the cascades below.';
COMMENT ON TABLE rubrik_operations IS
    'Durable Rubrik write records keyed by the client operation id. Kept when a mapping or cluster goes, as the record of what was sent. request and source hold no secrets.';
COMMENT ON TABLE rubrik_compliance_states IS
    'The poller''s persisted SLA compliance judgment per RSC object. The API reads it; a missing row means unknown.';
COMMENT ON TABLE rubrik_poller_events IS
    'Notification-worthy Rubrik transitions. UNIQUE (cluster_id, kind, dedup_key) makes each one recorded once across replicas. An empty namespace makes the event admin-only. detail is PRIVILEGED.';
COMMENT ON TABLE rubrik_poller_deliveries IS
    'Durable notification intents, one per event, so a restart between the state commit and the send leaves exactly one pending row.';
COMMENT ON TABLE rubrik_poller_lease IS
    'Rubrik poller work suppression. Advisory only: correctness comes from the events UNIQUE index.';
