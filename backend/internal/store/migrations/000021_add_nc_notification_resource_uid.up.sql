-- Notification dedup identity (pre-existing defect #2, 2026-10-02).
--
-- The dedup key was (source, resource_kind, resource_ns, resource_name,
-- title). It omitted the cluster and the object UID, so on a multi-cluster
-- install a same-named resource failing on a second cluster was suppressed as
-- a duplicate of the first, and a resource deleted and recreated under the
-- same name inherited its predecessor's suppression window.
--
-- cluster_id has existed since 000007 (TEXT NOT NULL DEFAULT ''); the fix
-- only starts reading it in the dedup query, where '' folds to the local
-- cluster. The UID had no column, so this migration adds one.
--
-- Additive: a new column with a constant default. PostgreSQL 11+ records the
-- default in the catalog instead of rewriting the table, so this takes a
-- brief ACCESS EXCLUSIVE lock and no scan. Every existing row reads
-- resource_uid = '' -- "no UID recorded" -- which is also what sources
-- without a UID write going forward. No backfill: a past notification's UID
-- cannot be recovered, and '' is exactly the legacy meaning.
--
-- No index change. idx_nc_notif_dedup (source, resource_kind, resource_ns,
-- resource_name, title, created_at DESC) still serves the lookup's equality
-- prefix and window range; cluster_id and resource_uid are residual filters
-- over the few rows sharing that prefix inside a 15-minute window.
ALTER TABLE nc_notifications
    ADD COLUMN IF NOT EXISTS resource_uid TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN nc_notifications.resource_uid IS
    'Kubernetes UID (or source-native stable id, e.g. Alertmanager fingerprint) of the resource; '''' when the source has none. Part of the dedup identity with cluster_id since 000021.';
