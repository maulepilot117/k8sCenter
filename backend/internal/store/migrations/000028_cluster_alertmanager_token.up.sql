-- Remote-cluster Alertmanager token (#608 PR 4b). Migration 000028.
-- Adds an optional encrypted bearer token for the cluster Alertmanager and the
-- origin it was entered for, so the store can refuse to return the token once
-- the Alertmanager URL moves to another origin. Additive, no backfill.
-- Re-runnable (ADD COLUMN IF NOT EXISTS, COMMENT ON); see NOTES.txt (000028).

ALTER TABLE cluster_monitoring
    ADD COLUMN IF NOT EXISTS alertmanager_token BYTEA,
    ADD COLUMN IF NOT EXISTS alertmanager_token_origin TEXT;

COMMENT ON COLUMN cluster_monitoring.alertmanager_token IS
    'Optional bearer token for the cluster Alertmanager, AES-256-GCM encrypted with the credentials master secret (store.Encrypt). Never returned by the API. Only valid while alertmanager_token_origin matches the origin of alertmanager_url. See NOTES.txt (000028).';
COMMENT ON COLUMN cluster_monitoring.alertmanager_token_origin IS
    'Origin (scheme://host:port) of the Alertmanager URL the token was entered for. An empty or NULL value, or a mismatch with alertmanager_url, means the token is treated as absent. See NOTES.txt (000028).';
