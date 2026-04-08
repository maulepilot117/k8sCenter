CREATE TABLE IF NOT EXISTS compliance_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    snapshot_date   DATE NOT NULL,
    cluster_id      TEXT NOT NULL DEFAULT 'local',
    overall_score   DOUBLE PRECISION NOT NULL CHECK (overall_score >= 0 AND overall_score <= 100),
    payload         JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_compliance_snapshot_unique
    ON compliance_snapshots (cluster_id, snapshot_date);
