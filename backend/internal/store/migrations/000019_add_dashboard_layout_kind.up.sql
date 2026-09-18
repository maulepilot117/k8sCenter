-- Personal dashboard layouts (Release G; spec 2026-09-13, D-11).
--
-- Additive: one new value in the kind CHECK. No column changes, no backfill,
-- no data migration. Every existing row keeps its kind and is untouched.
--
-- No new unique index is needed. idx_user_preferences_dedup already covers
-- (owner_id, kind, cluster_id, dedup_key); dashboard layouts set dedup_key to
-- the scope ("overview"), so one layout per user per cluster per scope falls
-- out of the constraint that is already there.
--
-- The CHECK is dropped and recreated rather than widened in place because
-- PostgreSQL has no ALTER CONSTRAINT for a CHECK expression. The recreate takes
-- an ACCESS EXCLUSIVE lock on user_preferences for the duration of a full-table
-- validation scan. On any realistic preferences table that is milliseconds; on
-- a very large one, run it in a maintenance window.
--
-- The name: 000018 declared this one inline on the column
-- (`kind TEXT NOT NULL CHECK (...)`) rather than naming it, so PostgreSQL
-- generated `user_preferences_kind_check` from the table and column names.
-- Every other CHECK on the table was named explicitly there, so nothing else
-- can hold that name.

ALTER TABLE user_preferences
    DROP CONSTRAINT IF EXISTS user_preferences_kind_check;

ALTER TABLE user_preferences
    ADD CONSTRAINT user_preferences_kind_check
    CHECK (kind IN ('saved_view', 'pin', 'dashboard_layout'));
