-- Drops the three columns 000027 added. cluster_monitoring rows and their
-- prometheus_url are kept; the stored bearer tokens and Alertmanager URLs are
-- lost, so a binding that needed a token stops authenticating until the token
-- is re-entered after a roll-forward. See NOTES.txt (000027).
ALTER TABLE cluster_monitoring
    DROP COLUMN IF EXISTS prometheus_token,
    DROP COLUMN IF EXISTS alertmanager_url,
    DROP COLUMN IF EXISTS updated_at;
