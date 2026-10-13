-- Drops the two columns 000028 added. cluster_monitoring rows, their
-- prometheus_url, prometheus_token and alertmanager_url are kept; every stored
-- Alertmanager token is lost and must be re-entered after a roll-forward.
-- See NOTES.txt (000028).
ALTER TABLE cluster_monitoring
    DROP COLUMN IF EXISTS alertmanager_token,
    DROP COLUMN IF EXISTS alertmanager_token_origin;
