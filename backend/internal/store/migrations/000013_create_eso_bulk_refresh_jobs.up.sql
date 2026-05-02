CREATE TABLE IF NOT EXISTS eso_bulk_refresh_jobs (
    id            UUID PRIMARY KEY,
    cluster_id    TEXT NOT NULL DEFAULT 'local',
    requested_by  TEXT NOT NULL,
    action        TEXT NOT NULL CHECK (action IN ('refresh_store', 'refresh_cluster_store', 'refresh_namespace')),
    scope_target  TEXT NOT NULL,
    target_uids   TEXT[] NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ,
    succeeded     TEXT[] NOT NULL DEFAULT '{}',
    failed        JSONB  NOT NULL DEFAULT '[]',
    skipped       JSONB  NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_eso_bulk_refresh_jobs_cluster_created
    ON eso_bulk_refresh_jobs (cluster_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_eso_bulk_refresh_jobs_active
    ON eso_bulk_refresh_jobs (cluster_id, action, scope_target)
    WHERE completed_at IS NULL;

COMMENT ON TABLE eso_bulk_refresh_jobs IS
    'ESO Phase E bulk-refresh job records. Scope is pinned at creation (target_uids); worker patches each ES one at a time. completed_at set on finish or shutdown so no orphaned IN-PROGRESS rows survive a restart. cluster_id matches clusters.id (TEXT) per platform precedent.';
