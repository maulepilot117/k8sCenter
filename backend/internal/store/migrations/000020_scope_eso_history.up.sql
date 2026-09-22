-- Release B / U13. Cluster-scope the ExternalSecret sync-history identity and
-- add the keyset-pagination index. No columns change: cluster_id has existed
-- since 000011 (NOT NULL DEFAULT 'local'), so there is nothing to backfill.
--
-- CONCURRENTLY is deliberately absent: golang-migrate runs each file inside a
-- transaction. Plain CREATE INDEX takes a SHARE lock; the single-replica ESO
-- poller is the only writer and retries a blocked Insert on its next 60s tick.

-- 1. Cluster-blind dedup -> cluster-scoped dedup. Widening only; cannot fail
--    on existing data (every row satisfying the old key satisfies the new one).
DROP INDEX IF EXISTS idx_eso_sync_history_dedup;
CREATE UNIQUE INDEX IF NOT EXISTS idx_eso_sync_history_dedup_cluster
    ON eso_sync_history (cluster_id, uid, attempt_at);

-- 2. Keyset pagination: matches the ORDER BY attempt_at DESC, id DESC tuple
--    walk in ESOHistoryStore.QueryPage exactly.
CREATE INDEX IF NOT EXISTS idx_eso_sync_history_keyset
    ON eso_sync_history (cluster_id, uid, attempt_at DESC, id DESC);

-- 3. Retire the now-redundant index: every query that used (uid, attempt_at)
--    now carries a cluster_id predicate, so it could only serve dead plans.
DROP INDEX IF EXISTS idx_eso_sync_history_uid_attempt;

COMMENT ON INDEX idx_eso_sync_history_dedup_cluster IS
    'Release B/U13: dedup key is (cluster_id, uid, attempt_at). A cluster restored from a Velero backup into a second registered cluster reproduces identical object UIDs; the pre-000020 (uid, attempt_at) key silently merged the two timelines.';
