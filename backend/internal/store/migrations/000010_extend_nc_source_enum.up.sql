-- Extend nc_notifications.source CHECK constraint to cover all sources the Go
-- enum (internal/notifications/types.go) emits.
--
-- The 000007 migration shipped with seven sources; the Go enum has since
-- grown velero, certmanager, limits, and now external_secrets. Without this
-- migration any INSERT carrying one of those values fails the CHECK at runtime.
--
-- golang-migrate wraps each migration in a transaction by default, so the
-- DROP+ADD is atomic.

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
