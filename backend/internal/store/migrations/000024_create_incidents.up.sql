-- Release D (U21a) - persistent incident investigations. Migration 000024.
-- Q1 policy: personal ownership + explicit collaborator grants; every read
-- re-checks the caller's CURRENT Kubernetes authorization for each evidence
-- item's stored scope (a grant alone never suffices). 30-day configurable
-- retention. Stricter gating for Secret-derived evidence.

CREATE TABLE IF NOT EXISTS incidents (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id                  TEXT        NOT NULL,
    cluster_id                TEXT        NOT NULL DEFAULT 'local',
    title                     TEXT        NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    summary                   TEXT        NOT NULL DEFAULT '' CHECK (length(summary) <= 10000),
    status                    TEXT        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    window_start              TIMESTAMPTZ NOT NULL,
    window_end                TIMESTAMPTZ,
    evidence_bytes            BIGINT      NOT NULL DEFAULT 0
                                  CHECK (evidence_bytes >= 0 AND evidence_bytes <= 10485760),
    evidence_count            INTEGER     NOT NULL DEFAULT 0
                                  CHECK (evidence_count >= 0 AND evidence_count <= 500),
    scope_count               INTEGER     NOT NULL DEFAULT 0
                                  CHECK (scope_count >= 0 AND scope_count <= 20),
    retention_days_at_capture INTEGER     NOT NULL CHECK (retention_days_at_capture BETWEEN 1 AND 3650),
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at                 TIMESTAMPTZ,
    CHECK (window_end IS NULL OR window_end >= window_start),
    CHECK (status <> 'closed' OR closed_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_incidents_owner_created  ON incidents (owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_incidents_retention      ON incidents (created_at);
CREATE INDEX IF NOT EXISTS idx_incidents_cluster_status ON incidents (cluster_id, status, created_at DESC);

COMMENT ON TABLE incidents IS
    'Release D incident records. owner_id is auth.User.ID (provider-qualified TEXT, NOT a local_users FK) per the platform precedent set by nc_reads.user_id. Visibility = owner OR incident_grants row, AND per-item current Kubernetes authorization at read time (Q1). Retention: whole-incident DELETE by created_at using the configured window (default 30d); lowering the config retroactively deletes - see NOTES.txt.';

CREATE TABLE IF NOT EXISTS incident_evidence (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id         UUID        NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    evidence_kind       TEXT        NOT NULL
                            CHECK (evidence_kind IN ('diagnostic_check', 'object_summary', 'event_list')),
    mode                TEXT        NOT NULL CHECK (mode IN ('snapshot', 'live_link')),
    cluster_id          TEXT        NOT NULL,
    api_group           TEXT        NOT NULL DEFAULT '',
    resource            TEXT        NOT NULL,
    source_kind         TEXT        NOT NULL,
    namespace           TEXT        NOT NULL DEFAULT '',
    name                TEXT        NOT NULL,
    source_uid          TEXT        NOT NULL DEFAULT '',
    resource_version    TEXT        NOT NULL DEFAULT '',
    secret_derived      BOOLEAN     NOT NULL DEFAULT false,
    source_observed_at  TIMESTAMPTZ,
    collected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    completeness        TEXT        NOT NULL
                            CHECK (completeness IN ('complete', 'partial', 'failed', 'forbidden', 'timed_out')),
    completeness_detail TEXT        NOT NULL DEFAULT '' CHECK (length(completeness_detail) <= 2000),
    redaction           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    payload             JSONB,
    payload_bytes       INTEGER     NOT NULL DEFAULT 0
                            CHECK (payload_bytes >= 0 AND payload_bytes <= 1048576),
    capture_key         TEXT        NOT NULL,
    CHECK ((mode = 'snapshot'  AND payload IS NOT NULL)
        OR (mode = 'live_link' AND payload IS NULL AND payload_bytes = 0))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_incident_evidence_dedup
    ON incident_evidence (incident_id, capture_key);
CREATE INDEX IF NOT EXISTS idx_incident_evidence_timeline
    ON incident_evidence (incident_id, collected_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_incident_evidence_scope
    ON incident_evidence (incident_id, cluster_id, api_group, resource, namespace);
CREATE INDEX IF NOT EXISTS idx_incident_evidence_provenance
    ON incident_evidence (cluster_id, source_uid);
CREATE INDEX IF NOT EXISTS idx_incident_evidence_secret
    ON incident_evidence (incident_id) WHERE secret_derived;

COMMENT ON TABLE incident_evidence IS
    'Append-only evidence. Provenance is keyed on (cluster_id, source_uid) - NEVER on (namespace, name), so a deleted-and-recreated object with a reused name inherits nothing (KTD3/R1). mode=snapshot stores an immutable redacted payload; mode=live_link stores only a reference resolved at read time. Values from core/secrets are never persisted; secret_derived rows additionally require get on secrets in the same namespace at read time.';

CREATE TABLE IF NOT EXISTS incident_notes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID        NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    author_id   TEXT        NOT NULL,
    body        TEXT        NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    revision    INTEGER     NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_incident_notes_incident ON incident_notes (incident_id, created_at);

COMMENT ON TABLE incident_notes IS
    'Investigation notes; the only mutable incident content. author_id is auth.User.ID (provider-qualified TEXT, not a local_users FK). Edit and delete are author-only and every mutation is bound to (incident_id, id), so a note id from another incident is never addressable. Each edit increments revision and appends the prior body to incident_note_revisions.';

CREATE TABLE IF NOT EXISTS incident_note_revisions (
    note_id    UUID        NOT NULL REFERENCES incident_notes(id) ON DELETE CASCADE,
    revision   INTEGER     NOT NULL,
    author_id  TEXT        NOT NULL,
    body       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, revision)
);

COMMENT ON TABLE incident_note_revisions IS
    'Prior bodies of an edited note, append-only. Written in the same transaction as the UPDATE that replaces the body, under a row lock on the note; a stale expected revision aborts that transaction before any write and the API returns 409 note_revision_conflict with the current revision.';

CREATE TABLE IF NOT EXISTS incident_grants (
    incident_id  UUID        NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    grantee_id   TEXT        NOT NULL,
    granted_by   TEXT        NOT NULL,
    can_annotate BOOLEAN     NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (incident_id, grantee_id)
);

CREATE INDEX IF NOT EXISTS idx_incident_grants_grantee ON incident_grants (grantee_id, incident_id);

COMMENT ON TABLE incident_grants IS
    'Explicit, revocable collaborator grants. A grant conveys standing to ask, never Kubernetes authority: every evidence read still re-checks the caller current authorization for the item stored scope (Q1 P2).';
