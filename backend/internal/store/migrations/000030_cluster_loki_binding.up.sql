-- Remote-cluster Loki binding (remote logs and shells U10, KTD8). Migration
-- 000030. Adds an optional Loki URL, an encrypted bearer token, the origin
-- that token was entered for, an optional tenant id and the binding's own
-- updated_at to cluster_monitoring, beside the metrics binding. Additive, no
-- backfill. Re-runnable (ADD COLUMN IF NOT EXISTS, COMMENT ON); see NOTES.txt
-- (000030).

ALTER TABLE cluster_monitoring
    ADD COLUMN IF NOT EXISTS loki_url TEXT,
    ADD COLUMN IF NOT EXISTS loki_token BYTEA,
    ADD COLUMN IF NOT EXISTS loki_token_origin TEXT,
    ADD COLUMN IF NOT EXISTS loki_tenant_id TEXT,
    ADD COLUMN IF NOT EXISTS loki_updated_at TIMESTAMPTZ;

COMMENT ON COLUMN cluster_monitoring.loki_url IS
    'Optional Loki base URL for the cluster. A non-empty value is a Loki binding, independent of prometheus_url. See NOTES.txt (000030).';
COMMENT ON COLUMN cluster_monitoring.loki_token IS
    'Optional bearer token for the cluster Loki, AES-256-GCM encrypted with the credentials master secret (store.Encrypt). Never returned by the API. Only valid while loki_token_origin matches the origin of loki_url. See NOTES.txt (000030).';
COMMENT ON COLUMN cluster_monitoring.loki_token_origin IS
    'Origin (scheme://host:port) of the Loki URL the token was entered for. An empty or NULL value, or a mismatch with loki_url, means the token is treated as absent. See NOTES.txt (000030).';
COMMENT ON COLUMN cluster_monitoring.loki_tenant_id IS
    'Optional Loki tenant, sent as X-Scope-OrgID. Not a secret. See NOTES.txt (000030).';
COMMENT ON COLUMN cluster_monitoring.loki_updated_at IS
    'Set on every Loki binding write, so a pod can notice a write another pod made. updated_at stays the metrics binding''s. See NOTES.txt (000030).';
