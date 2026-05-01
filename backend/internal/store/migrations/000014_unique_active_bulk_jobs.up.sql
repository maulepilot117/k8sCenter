-- Phase E follow-up (todo #347): replace the non-unique partial index with a
-- UNIQUE one so the database enforces "at most one in-flight job per
-- (cluster_id, action, scope_target)". Closes the FindActive→Insert TOCTOU
-- where two concurrent POSTs both observed an empty result and both inserted
-- a job, doubling the patch fan-out at provider rate-quota expense.

DROP INDEX IF EXISTS idx_eso_bulk_refresh_jobs_active;

CREATE UNIQUE INDEX IF NOT EXISTS idx_eso_bulk_refresh_jobs_active_unique
    ON eso_bulk_refresh_jobs (cluster_id, action, scope_target)
    WHERE completed_at IS NULL;
