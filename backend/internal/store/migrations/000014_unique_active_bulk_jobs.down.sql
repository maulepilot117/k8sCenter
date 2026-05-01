DROP INDEX IF EXISTS idx_eso_bulk_refresh_jobs_active_unique;

CREATE INDEX IF NOT EXISTS idx_eso_bulk_refresh_jobs_active
    ON eso_bulk_refresh_jobs (cluster_id, action, scope_target)
    WHERE completed_at IS NULL;
