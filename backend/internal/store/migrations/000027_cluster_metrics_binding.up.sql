-- Remote-cluster metrics binding (#608 PR 4a). Migration 000027.
-- Reuses the cluster_monitoring table created (and never written) since
-- 000003: a row with a non-empty prometheus_url is a per-cluster metrics
-- binding. Adds the encrypted bearer token for that Prometheus, the
-- Alertmanager URL (stored now, consumed later) and an updated_at stamp.
-- Additive, no backfill. Re-runnable (ADD COLUMN IF NOT EXISTS, COMMENT ON);
-- see NOTES.txt (000027).

ALTER TABLE cluster_monitoring
    ADD COLUMN IF NOT EXISTS prometheus_token BYTEA,
    ADD COLUMN IF NOT EXISTS alertmanager_url TEXT,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

COMMENT ON COLUMN cluster_monitoring.prometheus_token IS
    'Optional bearer token for the cluster Prometheus, AES-256-GCM encrypted with the credentials master secret (store.Encrypt). Never returned by the API. See NOTES.txt (000027).';
