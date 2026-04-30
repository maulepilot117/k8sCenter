CREATE TABLE IF NOT EXISTS eso_sync_history (
    id                       BIGSERIAL PRIMARY KEY,
    cluster_id               TEXT NOT NULL DEFAULT 'local',
    uid                      TEXT NOT NULL,
    namespace                TEXT NOT NULL,
    name                     TEXT NOT NULL,
    attempt_at               TIMESTAMPTZ NOT NULL,
    outcome                  TEXT NOT NULL CHECK (outcome IN ('success', 'failure', 'partial')),
    reason                   TEXT NOT NULL DEFAULT '',
    message                  TEXT NOT NULL DEFAULT '',
    diff_keys_added          TEXT[] NOT NULL DEFAULT '{}',
    diff_keys_removed        TEXT[] NOT NULL DEFAULT '{}',
    diff_keys_changed        TEXT[] NOT NULL DEFAULT '{}',
    synced_resource_version  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_eso_sync_history_uid_attempt
    ON eso_sync_history (uid, attempt_at DESC);

CREATE INDEX IF NOT EXISTS idx_eso_sync_history_cluster_failures
    ON eso_sync_history (cluster_id, attempt_at DESC)
    WHERE outcome <> 'success';

CREATE UNIQUE INDEX IF NOT EXISTS idx_eso_sync_history_dedup
    ON eso_sync_history (uid, attempt_at);

COMMENT ON TABLE eso_sync_history IS
    'Per-ExternalSecret sync attempt history. UID-keyed (R8); 90-day retention via DELETE-WHERE goroutine. Partition candidate: if steady-state row count exceeds 10M, migrate to monthly RANGE partitioning on attempt_at.';
