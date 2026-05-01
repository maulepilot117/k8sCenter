-- Phase C follow-up: add a standalone (attempt_at) index so the hourly
-- retention DELETE doesn't fall back to a sequential scan.
--
-- The migration 000011 indexes all lead with `uid` or `cluster_id`, so the
-- predicate `WHERE attempt_at < NOW() - INTERVAL '90 days'` had no usable
-- B-tree to walk. At the plan's 2.16M-row steady state, a seq scan plus a
-- table-wide DELETE under write pressure could pin a pgxpool connection
-- for minutes. The standalone index also serves as the natural partition
-- key column when the 10M-row threshold (noted in the table COMMENT) is
-- reached, making any future RANGE-partitioning migration cheaper.
CREATE INDEX IF NOT EXISTS idx_eso_sync_history_attempt_at
    ON eso_sync_history (attempt_at);
