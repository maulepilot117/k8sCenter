-- Drops only the create idempotency key: the partial unique index, then the
-- column. Every incident, its evidence, notes and grants are kept. Effect: a
-- create retried after the rollback can no longer be matched to its first
-- attempt and may make a duplicate incident. See NOTES.txt (000026).
DROP INDEX IF EXISTS idx_incidents_owner_client_request;
ALTER TABLE incidents DROP COLUMN IF EXISTS client_request_id;
