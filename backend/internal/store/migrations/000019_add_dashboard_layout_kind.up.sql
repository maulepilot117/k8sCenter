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
--
-- That name is an assumption about a database this file cannot see, so it is
-- checked rather than trusted. Dropping a CHECK by a name nothing holds is a
-- silent no-op: the ADD below would then succeed, the migration would record
-- clean, and the original narrow constraint would still be there rejecting
-- every dashboard layout -- a failure that only shows up much later, as writes
-- that should work but do not. Failing here instead turns that into one loud
-- error at deploy time, naming what to do about it.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'user_preferences'::regclass
           AND conname  = 'user_preferences_kind_check'
    ) THEN
        RAISE EXCEPTION
            'user_preferences_kind_check not found: the kind CHECK is under a different name on this database, so dropping it by name would silently leave the narrow constraint in force. Find it with: SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = ''user_preferences''::regclass AND contype = ''c''; drop that constraint, then re-run this migration.';
    END IF;
END
$$;

-- Plain DROP, not DROP IF EXISTS: the guard above has already established that
-- it is there, and IF EXISTS is exactly the silence this migration must not have.
ALTER TABLE user_preferences
    DROP CONSTRAINT user_preferences_kind_check;

ALTER TABLE user_preferences
    ADD CONSTRAINT user_preferences_kind_check
    CHECK (kind IN ('saved_view', 'pin', 'dashboard_layout'));
