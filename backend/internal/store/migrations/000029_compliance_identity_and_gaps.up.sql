-- Remote compliance history (#608 final slice). Migration 000029.
-- Adds the background identity a remote cluster's daily compliance snapshot
-- impersonates (cluster_compliance_identity, removed with the cluster), the
-- sequence its revision is drawn from, and the table that records days a
-- snapshot could not be taken (compliance_snapshot_gaps). Does not touch
-- compliance_snapshots. Additive, no backfill. Re-runnable (IF NOT EXISTS,
-- COMMENT ON); see NOTES.txt (000029).

CREATE SEQUENCE IF NOT EXISTS cluster_compliance_identity_revision_seq;

CREATE TABLE IF NOT EXISTS cluster_compliance_identity (
    cluster_id TEXT PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    username   TEXT NOT NULL,
    groups     TEXT[] NOT NULL DEFAULT '{}',
    revision   BIGINT NOT NULL,
    updated_by TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS compliance_snapshot_gaps (
    cluster_id        TEXT NOT NULL,
    snapshot_date     DATE NOT NULL,
    reason            TEXT NOT NULL,
    identity_revision BIGINT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, snapshot_date)
);

COMMENT ON TABLE cluster_compliance_identity IS
    'Background identity (username and groups) the remote compliance recorder impersonates for a cluster. Not a secret. One row per cluster; deleting the cluster deletes it. See NOTES.txt (000029).';
COMMENT ON COLUMN cluster_compliance_identity.revision IS
    'Drawn from cluster_compliance_identity_revision_seq whenever username or groups change. Never reused, so it survives a clear and a re-name. See NOTES.txt (000029).';
COMMENT ON TABLE compliance_snapshot_gaps IS
    'Days a remote cluster compliance snapshot could not be taken, with the reason and the identity revision in force. No FK: the cluster delete removes these rows in its own transaction. See NOTES.txt (000029).';
