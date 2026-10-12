-- Drops the two tables and the sequence 000029 created. Every stored
-- compliance identity and every recorded gap is lost; remote clusters stop
-- recording history until an identity is saved again after a roll-forward.
-- compliance_snapshots is not touched. See NOTES.txt (000029).
DROP TABLE IF EXISTS compliance_snapshot_gaps;
DROP TABLE IF EXISTS cluster_compliance_identity;
DROP SEQUENCE IF EXISTS cluster_compliance_identity_revision_seq;
