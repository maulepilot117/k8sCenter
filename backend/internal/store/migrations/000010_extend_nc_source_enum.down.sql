-- Restores the original 000007 source list. Note: any rows added with sources
-- outside the original seven (velero / certmanager / limits / external_secrets)
-- must be deleted or migrated by the operator before downgrading; the
-- constraint will reject any row that doesn't match.

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
