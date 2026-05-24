-- F#5 (security audit 2026-05-22) — adds an explicit allow_insecure_tls flag
-- to the clusters table so the silent TLS-disable fallback can be replaced
-- with a fail-closed check. cluster_router.buildRemoteConfig previously set
-- TLSClientConfig.Insecure = true whenever no CA data was stored. That is
-- the right behavior for homelab self-signed setups but only when the
-- operator explicitly opted in. Default-deny on new rows: any cluster
-- registered AFTER the migration without CA data starts returning an error
-- until an admin toggles the flag on intentionally.

ALTER TABLE clusters
    ADD COLUMN IF NOT EXISTS allow_insecure_tls boolean NOT NULL DEFAULT false;

-- F#3 (round-2 follow-up) — backfill existing CA-less rows. Without this
-- backfill, every cluster that was registered before the migration without
-- a CA certificate would immediately stop working on upgrade (the router
-- would refuse to build a client). Preserving the pre-upgrade silent-
-- insecure behavior keeps existing deployments connected; the operator can
-- review the auto-flagged rows in the UI (badge), flip them back to false
-- explicitly, or supply a real CA. Surfacing the implicit choice with the
-- "⚠ Insecure TLS" badge in the cluster list is the visibility half of the
-- defense.
UPDATE clusters
    SET allow_insecure_tls = true
    WHERE (ca_data IS NULL OR length(ca_data) = 0)
      AND is_local = false;
