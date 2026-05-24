-- F#5 (security audit 2026-05-22) — adds an explicit allow_insecure_tls flag
-- to the clusters table so the silent TLS-disable fallback can be replaced
-- with a fail-closed check. cluster_router.buildRemoteConfig previously set
-- TLSClientConfig.Insecure = true whenever no CA data was stored. That is
-- the right behavior for homelab self-signed setups but only when the
-- operator explicitly opted in. Default-deny on new rows: any cluster
-- registered AFTER the migration without CA data starts returning an error
-- until an admin toggles the flag on intentionally.
--
-- F#2 (round-3): the backfill UPDATE that originally lived here has been
-- moved into a dedicated migration 000017_backfill_allow_insecure_tls.
-- golang-migrate skips already-applied migration versions, so editing
-- 000016 in place after the fact would NOT re-run the backfill on
-- deployments that had already applied the pre-backfill version of 000016.
-- Splitting the backfill into 000017 makes the upgrade re-runnable for
-- every deployment regardless of which round they're coming from.

ALTER TABLE clusters
    ADD COLUMN IF NOT EXISTS allow_insecure_tls boolean NOT NULL DEFAULT false;
