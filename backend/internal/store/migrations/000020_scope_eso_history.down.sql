-- ROLLBACK HAZARD -- read migrations/NOTES.txt before running.
-- Recreating the stricter UNIQUE (uid, attempt_at) FAILS if two clusters ever
-- recorded the same (uid, attempt_at). Find offenders first:
--
--   SELECT uid, attempt_at, count(DISTINCT cluster_id)
--     FROM eso_sync_history GROUP BY 1,2 HAVING count(DISTINCT cluster_id) > 1;
--
-- There is no automatic resolution: deleting either side destroys one cluster's
-- audit trail. The operator must choose and delete explicitly.
DROP INDEX IF EXISTS idx_eso_sync_history_keyset;
CREATE INDEX IF NOT EXISTS idx_eso_sync_history_uid_attempt
    ON eso_sync_history (uid, attempt_at DESC);
DROP INDEX IF EXISTS idx_eso_sync_history_dedup_cluster;
CREATE UNIQUE INDEX IF NOT EXISTS idx_eso_sync_history_dedup
    ON eso_sync_history (uid, attempt_at);
