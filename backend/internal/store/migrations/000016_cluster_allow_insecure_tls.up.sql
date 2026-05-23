-- F#5 (security audit 2026-05-22) — adds an explicit allow_insecure_tls flag
-- to the clusters table so the silent TLS-disable fallback can be replaced
-- with a fail-closed check. cluster_router.buildRemoteConfig previously set
-- TLSClientConfig.Insecure = true whenever no CA data was stored. That is
-- the right behavior for homelab self-signed setups but only when the
-- operator explicitly opted in. Default-deny: existing rows get FALSE so
-- any cluster without CA data starts returning an error until an admin
-- toggles the flag on intentionally.

ALTER TABLE clusters
    ADD COLUMN IF NOT EXISTS allow_insecure_tls boolean NOT NULL DEFAULT false;
