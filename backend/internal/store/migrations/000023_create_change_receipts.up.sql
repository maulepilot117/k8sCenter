-- Release E (U27, KTD9). Durable operation intent and receipts for tracked
-- YAML applies. Two tables:
--   change_receipts       -- one row per tracked apply; id is the CLIENT-supplied
--                            operation id (UUIDv4) and doubles as the idempotency key
--   change_receipt_grants -- explicit read grants (Q1: ownership plus grants)
-- Style follows 000011/000013/000022: IF NOT EXISTS, TEXT + CHECK enums rather
-- than PG enums, TIMESTAMPTZ, partial index, COMMENT ON TABLE.

CREATE TABLE IF NOT EXISTS change_receipts (
    id                  UUID PRIMARY KEY,
    owner_id            TEXT NOT NULL,
    owner_username      TEXT NOT NULL DEFAULT '',
    cluster_id          TEXT NOT NULL DEFAULT 'local',
    cluster_generation  TEXT NOT NULL DEFAULT '',
    content_digest      TEXT NOT NULL,
    document_count      INTEGER NOT NULL,
    force               BOOLEAN NOT NULL DEFAULT false,
    contains_secret     BOOLEAN NOT NULL DEFAULT false,
    repair_of           UUID,
    state               TEXT NOT NULL DEFAULT 'applying'
        CHECK (state IN ('previewed', 'applying', 'applied', 'partial', 'failed', 'unknown')),
    objects             JSONB NOT NULL DEFAULT '[]',
    ownership           JSONB NOT NULL DEFAULT '[]',
    verification_state  TEXT NOT NULL DEFAULT 'pending'
        CHECK (verification_state IN ('pending', 'verifying', 'verified', 'inconclusive', 'verification_failed')),
    verification        JSONB NOT NULL DEFAULT '[]',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    mutation_started_at TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    verified_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_change_receipts_owner_created
    ON change_receipts (owner_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_change_receipts_cluster_created
    ON change_receipts (cluster_id, created_at DESC);

-- Recovery scan on boot: every row that never reached a terminal state.
CREATE INDEX IF NOT EXISTS idx_change_receipts_unfinished
    ON change_receipts (created_at)
    WHERE completed_at IS NULL;

CREATE TABLE IF NOT EXISTS change_receipt_grants (
    receipt_id   UUID NOT NULL REFERENCES change_receipts(id) ON DELETE CASCADE,
    grantee_id   TEXT NOT NULL,
    granted_by   TEXT NOT NULL,
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (receipt_id, grantee_id)
);

CREATE INDEX IF NOT EXISTS idx_change_receipt_grants_grantee
    ON change_receipt_grants (grantee_id);

COMMENT ON TABLE change_receipts IS
    'Release E tracked-apply receipts. id is the CLIENT-supplied idempotency key (UUIDv4). Stores a sha256 digest of the submitted bundle and per-object references/outcomes only -- NEVER manifest content, so a Secret-bearing apply cannot leak through this table. owner_id is auth.User.ID (provider-qualified for oidc/ldap, opaque for local). cluster_generation is the YAML target pin generation at apply time (TargetSchema.Generation: the literal local for the local cluster, the cluster record created_at for a remote one), used to detect delete-and-re-register. state and verification_state advance independently: applied + pending is a legal, common combination. Rows still applying with completed_at IS NULL past a grace period are reconciled to failed (never mutated) or unknown (mutation started) -- never replayed. 30-day retention via DELETE-WHERE sweep.';

COMMENT ON TABLE change_receipt_grants IS
    'Explicit read grants on a change receipt (Q1 policy: ownership plus explicit grants). Release E honours existing rows on read but ships no grant-creation API; grant management lands with the Release D incident-sharing surface.';
