-- Drops only the single-row retention-state table. Incidents, evidence, notes and
-- grants are NOT touched. Effect: the next start has no recorded applied retention,
-- so the lowering guard falls back to its first-run rule (defer the first sweep when
-- incidents are already past the configured window, unless
-- KUBECENTER_INCIDENTS_RETENTIONLOWERINGCONFIRMED=true). See NOTES.txt (000025).
DROP TABLE IF EXISTS incident_retention_state;
