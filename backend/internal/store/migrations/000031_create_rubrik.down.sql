-- Reverses 000031: restores the 000010 notification source list and drops
-- every Rubrik table. All Rubrik data is lost: the connection secret must be
-- entered again and clusters mapped again after a roll-forward. See
-- NOTES.txt (000031).
--
-- Step 1: refuse to proceed while 'rubrik' notifications exist, because
-- adding the narrower CHECK back validates existing rows. The refusal names
-- the cause instead of a bare constraint error. Run through the migrator,
-- either error leaves the version DIRTY; NOTES.txt (000031) says how to
-- delete the rows and reset the version.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM nc_notifications WHERE source = 'rubrik' LIMIT 1) THEN
        RAISE EXCEPTION
            'Cannot apply 000031 down: nc_notifications contains rows with source rubrik. Delete them first (see backend/internal/store/migrations/NOTES.txt, 000031).';
    END IF;
END $$;

-- Step 2: strip 'rubrik' from rule source filters, which the rolled-back
-- dispatcher would otherwise silently ignore. A rule that targeted only
-- rubrik is disabled first: an empty filter matches every source, so
-- stripping alone would send its channel every notification.

UPDATE nc_rules
SET enabled = false, updated_at = now(), updated_by = 'migration-000031-down'
WHERE enabled
  AND 'rubrik' = ANY(source_filter)
  AND NOT EXISTS (SELECT 1 FROM unnest(source_filter) AS s WHERE s <> 'rubrik');

UPDATE nc_rules
SET source_filter = ARRAY(
    SELECT s
    FROM unnest(source_filter) AS s
    WHERE s <> 'rubrik'
)
WHERE 'rubrik' = ANY(source_filter);

-- Step 3: restore the 000010 constraint.

ALTER TABLE nc_notifications DROP CONSTRAINT IF EXISTS nc_notifications_source_check;
ALTER TABLE nc_notifications ADD CONSTRAINT nc_notifications_source_check
    CHECK (source IN (
        'alert',
        'policy',
        'gitops',
        'diagnostic',
        'scan',
        'cluster',
        'audit',
        'velero',
        'certmanager',
        'limits',
        'external_secrets'
    ));

-- Step 4: drop the tables, dependents first.

DROP TABLE IF EXISTS rubrik_poller_deliveries;
DROP TABLE IF EXISTS rubrik_poller_events;
DROP TABLE IF EXISTS rubrik_compliance_states;
DROP TABLE IF EXISTS rubrik_poller_state;
DROP TABLE IF EXISTS rubrik_poller_lease;
DROP TABLE IF EXISTS rubrik_operations;
DROP TABLE IF EXISTS rubrik_cluster_mappings;
DROP TABLE IF EXISTS rubrik_connection;
