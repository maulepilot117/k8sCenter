-- Drops the five columns 000030 added. cluster_monitoring rows and their
-- metrics binding columns are kept; every Loki binding (URL, token, tenant)
-- is lost and must be entered again after a roll-forward. A row that held
-- only a Loki binding is left with no binding at all. See NOTES.txt (000030).
ALTER TABLE cluster_monitoring
    DROP COLUMN IF EXISTS loki_url,
    DROP COLUMN IF EXISTS loki_token,
    DROP COLUMN IF EXISTS loki_token_origin,
    DROP COLUMN IF EXISTS loki_tenant_id,
    DROP COLUMN IF EXISTS loki_updated_at;
