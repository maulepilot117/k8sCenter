-- Release D (U25c) - idempotent incident create. Migration 000026.
-- POST /incidents may carry a client-generated clientRequestId (UUID). The
-- unique index makes a create with the same id idempotent PER OWNER: a retry
-- whose first response was lost returns the incident the first attempt made
-- instead of a duplicate. The index is partial, so incidents created without
-- an id (NULL) never conflict. Additive, no backfill. Re-runnable
-- (ADD COLUMN IF NOT EXISTS, CREATE ... IF NOT EXISTS, COMMENT ON); see
-- NOTES.txt (000026).

ALTER TABLE incidents ADD COLUMN IF NOT EXISTS client_request_id UUID;

CREATE UNIQUE INDEX IF NOT EXISTS idx_incidents_owner_client_request
    ON incidents (owner_id, client_request_id)
    WHERE client_request_id IS NOT NULL;

COMMENT ON COLUMN incidents.client_request_id IS
    'Optional client-generated create idempotency key (POST /incidents clientRequestId). Unique per owner_id (partial index idx_incidents_owner_client_request); a replay returns the existing incident, first write wins. Never returned by the API. See NOTES.txt (000026).';
