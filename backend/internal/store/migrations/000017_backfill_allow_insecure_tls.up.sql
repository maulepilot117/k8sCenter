-- F#2 (round-3 security audit 2026-05-23) — backfill existing CA-less rows.
--
-- This migration exists because 000016 was edited in place during round-2
-- to add a backfill UPDATE alongside the column ADD. golang-migrate skips
-- migration versions that are already recorded in schema_migrations, so
-- deployments that had applied the round-1 version of 000016 (column ADD
-- only) would NOT have re-run the file after the in-place edit added the
-- backfill UPDATE. Result: those deployments would silently start failing
-- every remote-cluster API call as soon as the post-F#5 backend (which
-- enforces fail-closed TLS for CA-less rows) rolled out. Splitting the
-- backfill into a new migration version forces every deployment to run it
-- regardless of which round of 000016 they applied first.
--
-- Without this backfill, every cluster that was registered before F#5
-- without a CA certificate would immediately stop working on upgrade.
-- Preserving the pre-upgrade silent-insecure behavior keeps existing
-- deployments connected; the operator can review the auto-flagged rows
-- in the UI (badge), flip them back to false explicitly, or supply a real
-- CA. Surfacing the implicit choice with the "⚠ Insecure TLS" badge in
-- the cluster list is the visibility half of the defense.
--
-- The local cluster row (is_local = true) is intentionally skipped — the
-- in-cluster TLS path uses the service-account CA bundle from the pod,
-- not the stored ca_data column.

UPDATE clusters
    SET allow_insecure_tls = true
    WHERE (ca_data IS NULL OR length(ca_data) = 0)
      AND is_local = false;
