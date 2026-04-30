-- Restores the original 000007 source list. Adding the constrained CHECK
-- back validates against existing rows, so any nc_notifications row whose
-- source is in the post-000007 set (velero / certmanager / limits /
-- external_secrets) must be removed BEFORE this script runs — otherwise
-- ADD CONSTRAINT fails and golang-migrate marks the version DIRTY,
-- leaving the table without ANY source CHECK constraint at all.
--
-- Step 1: refuse to proceed if blocking rows exist. Operators must export
-- or delete those rows first; the explicit error is far less painful than
-- discovering a dirty schema mid-rollback.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM nc_notifications
        WHERE source IN ('velero','certmanager','limits','external_secrets')
        LIMIT 1
    ) THEN
        RAISE EXCEPTION
            'Cannot apply 000010 down: nc_notifications contains rows with post-000007 source values. Delete or re-source them first (see backend/internal/store/migrations/000010_extend_nc_source_enum.down.sql header).';
    END IF;
END $$;

-- Step 2: scrub nc_rules.source_filter arrays. Rules created during the
-- 000010 window may target external_secrets / velero / certmanager /
-- limits sources; after rollback the rolled-back binary's dispatcher
-- silently drops those targets. Strip them so rules that survive the
-- rollback remain operationally meaningful (or get removed entirely if
-- they have nothing else to match).

UPDATE nc_rules
SET source_filter = ARRAY(
    SELECT s
    FROM unnest(source_filter) AS s
    WHERE s NOT IN ('velero','certmanager','limits','external_secrets')
);

-- Step 3: replace the constraint with the original 7-source list.

ALTER TABLE nc_notifications DROP CONSTRAINT IF EXISTS nc_notifications_source_check;
ALTER TABLE nc_notifications ADD CONSTRAINT nc_notifications_source_check
    CHECK (source IN (
        'alert',
        'policy',
        'gitops',
        'diagnostic',
        'scan',
        'cluster',
        'audit'
    ));
