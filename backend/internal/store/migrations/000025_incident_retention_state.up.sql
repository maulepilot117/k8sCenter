-- Release D (U25a) - the retention a sweep actually applied. Migration 000025.
-- One row, upserted after every successful incident retention sweep. At start the
-- backend compares the configured retention with it to tell a NEW lowering (which
-- deletes incidents captured under a longer retention, so the first sweep is
-- deferred) from steady state. Re-runnable: CREATE ... IF NOT EXISTS, COMMENT ON.

CREATE TABLE IF NOT EXISTS incident_retention_state (
    id                     BOOLEAN     PRIMARY KEY DEFAULT true CHECK (id),
    applied_retention_days INTEGER     NOT NULL CHECK (applied_retention_days BETWEEN 1 AND 3650),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE incident_retention_state IS 'Single row: the incident retention (days) the last successful sweep applied. Lets startup distinguish a newly lowered retention from steady state. Not user data; see NOTES.txt (000025).';
