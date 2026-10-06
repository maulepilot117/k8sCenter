/**
 * Wire types for incident records, evidence, notes, grants, capture and
 * export (Release D).
 *
 * Every interface mirrors the JSON tags of the Go type named in its doc
 * comment, field for field:
 *   - backend/internal/incidents/handler.go         (IncidentView, EvidenceCounts,
 *                                                     IncidentSummary, IncidentDetail,
 *                                                     NoteView, request bodies, reasons)
 *   - backend/internal/incidents/handler_capture.go (CaptureResponse, EvidencePage,
 *                                                     capture request body)
 *   - backend/internal/incidents/handler_grants.go  (GrantView, grant request body)
 *   - backend/internal/incidents/handler_export.go  (ExportDocument, ExportTruncation)
 *   - backend/internal/incidents/collector.go       (Evidence, WithheldEvidence,
 *                                                     SourceRef, SourceReport, Completeness)
 *   - backend/internal/incidents/redaction.go       (RedactionMeta)
 *   - backend/internal/store/incidents.go, incident_evidence.go, incident_grants.go
 *     (status, kind, mode and completeness CHECK sets; text and grant bounds)
 *
 * Conventions follow `change-types.ts`: a set the database CHECKs is a closed
 * string-literal union (give every `switch` over one a neutral `default`, and
 * extend the union in the PR that adds the Go constant); error reasons are
 * `Open<Known>` because a newer server may add one. Go `time.Time` is an
 * RFC 3339 string; `*time.Time` with `omitempty` is an optional string. Slices
 * the handlers always construct are non-optional arrays.
 */

import type { Open } from "./change-types.ts";
import { getResourceSection, KIND_ROUTE_MAP } from "./types/diagnostics.ts";

// --- Bounds (store) -----------------------------------------------------------

/** store.IncidentMaxTitleChars: a title is 1..200 characters. */
export const INCIDENT_MAX_TITLE_CHARS = 200;
/** store.IncidentMaxSummaryChars: a summary is 0..10000 characters. */
export const INCIDENT_MAX_SUMMARY_CHARS = 10000;
/** store.IncidentMaxNoteBodyChars: a note body is 1..20000 characters. */
export const INCIDENT_MAX_NOTE_BODY_CHARS = 20000;
/** store.IncidentMaxGrants: grantees per incident. */
export const INCIDENT_MAX_GRANTS = 50;
/** store.IncidentMaxPageSize: the largest `limit` a list honours. */
export const INCIDENT_MAX_PAGE_SIZE = 200;

// --- Enumerations ------------------------------------------------------------

/** store.IncidentStatus* (incidents.status CHECK). */
export type IncidentStatus = "open" | "closed";

/** incidents.Role: how the CALLER qualifies to see the incident. */
export type IncidentRole = "owner" | "collaborator";

/** store.EvidenceKind* (incident_evidence CHECK). */
export type EvidenceKind = "diagnostic_check" | "object_summary" | "event_list";

/**
 * incidents.EvidenceMode. `snapshot` carries an immutable redacted payload;
 * `live_link` carries only a reference and may later 404 or 403.
 */
export type EvidenceMode = "snapshot" | "live_link";

/** incidents.Completeness: how much of what a source set out to observe it did. */
export type Completeness =
  | "complete"
  | "partial"
  | "failed"
  | "forbidden"
  | "timed_out";

/** The withheld reasons (Q1 P4). `authorization_check_unavailable` is retryable. */
export type WithheldReason = "forbidden" | "authorization_check_unavailable";

/** Capture source ids (incidents.Source*). Omitting `sources` runs all of them. */
export type CaptureSourceId = "diagnostics" | "object" | "events";

/** The kinds capture accepts: exactly the kinds diagnostics resolves. */
export type CaptureKind =
  | "Deployment"
  | "StatefulSet"
  | "DaemonSet"
  | "Pod"
  | "Service"
  | "PersistentVolumeClaim";

/** incidents.ExportFormat*. There is no HTML export. */
export type ExportFormat = "json" | "markdown";

/**
 * Machine-readable `error.reason` values the incident endpoints send. Open:
 * branch on the known ones and render anything else neutrally.
 */
export type IncidentErrorReason = Open<
  | "incident_persistence_unavailable"
  | "incident_store_unavailable"
  | "incident_busy"
  | "note_revision_conflict"
  | "incident_capture_unavailable"
  | "remote_capture_unsupported"
  | "evidence_limit_exceeded"
  | "scope_limit_exceeded"
  | "incident_closed"
  | "grant_limit_reached"
  | "export_format_invalid"
  | "incident_capture_outcome_unknown"
>;

// --- Records -------------------------------------------------------------------

/**
 * incidents.IncidentView. `role` and `canAnnotate` are the caller's standing,
 * so the UI can gate controls; the record carries no evidence totals.
 */
export interface IncidentView {
  id: string;
  ownerId: string;
  clusterId: string;
  title: string;
  summary: string;
  status: IncidentStatus;
  windowStart: string;
  windowEnd?: string;
  retentionDays: number;
  createdAt: string;
  updatedAt: string;
  closedAt?: string;
  role: IncidentRole;
  canAnnotate: boolean;
}

/** incidents.EvidenceCounts: whole-incident counts for the CALLER. */
export interface EvidenceCounts {
  visible: number;
  withheld: number;
}

/**
 * incidents.IncidentSummary (create and update). `counts` is absent only when
 * the counting read failed after the write committed.
 */
export interface IncidentSummary {
  incident: IncidentView;
  counts?: EvidenceCounts;
}

// --- Evidence ------------------------------------------------------------------

/** incidents.SourceRef: the captured object, and the item's authorization scope. */
export interface SourceRef {
  clusterId: string;
  /** "" for the core group. */
  apiGroup: string;
  /** Plural, lowercase API resource. */
  resource: string;
  /** Display only. */
  kind: string;
  namespace: string;
  name: string;
  uid?: string;
  resourceVersion?: string;
  /**
   * No UID could be observed: the item is bound to a name, and the UI must
   * say so rather than imply object identity (Q1 P9).
   */
  identityWeak?: boolean;
}

/** incidents.RedactionMeta. */
export interface RedactionMeta {
  applied: boolean;
  rules?: string[];
  fieldsRemoved: number;
  truncated: boolean;
  secretDerived: boolean;
}

/**
 * incidents.Evidence: an item the caller may read. The Go type has no
 * `withheld` field; `withheld?: undefined` here is what makes
 * `EvidenceItem` a discriminated union.
 */
export interface Evidence {
  withheld?: undefined;
  id: string;
  incidentId: string;
  evidenceKind: EvidenceKind;
  mode: EvidenceMode;
  source: SourceRef;
  /** When the cluster observed the fact; absent when the source has none. */
  sourceObservedAt?: string;
  /** When k8sCenter wrote it down. */
  collectedAt: string;
  completeness: Completeness;
  completenessDetail?: string;
  redaction: RedactionMeta;
  /** The redacted snapshot (json.RawMessage); absent for a live link. */
  payload?: unknown;
  payloadBytes: number;
}

/**
 * incidents.WithheldEvidence: the ONLY shape of an item the caller may not
 * read. It deliberately has no scope fields.
 */
export interface WithheldEvidence {
  id: string;
  evidenceKind: EvidenceKind;
  collectedAt: string;
  withheld: true;
  withheldReason: WithheldReason;
}

/**
 * One evidence entry, readable or withheld. Narrow on `withheld` before
 * touching any scope field: the withheld branch has none.
 */
export type EvidenceItem = Evidence | WithheldEvidence;

/** True when the item is a withheld placeholder. */
export function isWithheld(item: EvidenceItem): item is WithheldEvidence {
  return item.withheld === true;
}

/** incidents.EvidencePage (GET /incidents/{id}/evidence). */
export interface EvidencePage {
  counts: EvidenceCounts;
  evidence: Evidence[];
  withheld: WithheldEvidence[];
}

/**
 * incidents.IncidentDetail (GET /incidents/{id}): the record, the caller's
 * whole-incident counts, and one page of evidence with the page's withheld
 * placeholders.
 */
export interface IncidentDetail extends EvidencePage {
  incident: IncidentView;
}

// --- Notes and grants -----------------------------------------------------------

/** incidents.NoteView. */
export interface NoteView {
  id: string;
  incidentId: string;
  authorId: string;
  body: string;
  revision: number;
  createdAt: string;
  updatedAt: string;
}

/** incidents.GrantView. */
export interface GrantView {
  incidentId: string;
  granteeId: string;
  grantedBy: string;
  canAnnotate: boolean;
  createdAt: string;
}

// --- Capture -------------------------------------------------------------------

/** incidents.SourceReport: one source's outcome, with a fixed, scope-free detail. */
export interface SourceReport {
  id: Open<CaptureSourceId>;
  completeness: Completeness;
  detail?: string;
  items: number;
}

/** incidents.CaptureResponse. Items are not echoed; read them back through evidence. */
export interface CaptureResponse {
  completeness: Completeness;
  collectedAt: string;
  sources: SourceReport[];
  /** Items the collector returned. */
  collected: number;
  /** New rows written. */
  inserted: number;
  /** Items already present for this incident (same capture key), skipped. */
  deduplicated: number;
  /** Items that failed row validation and were dropped. */
  dropped: number;
}

// --- Export --------------------------------------------------------------------

/** incidents.ExportTruncation: what a truncated export left out. */
export interface ExportTruncation {
  evidenceOmitted: number;
  withheldOmitted: number;
  notesOmitted: boolean;
}

/** incidents.ExportDocument: the `format=json` export (a file, no envelope). */
export interface ExportDocument {
  schema: string;
  exportedAt: string;
  exportedBy: string;
  incident: IncidentView;
  counts: EvidenceCounts;
  withheldByReason: Record<string, number>;
  evidence: Evidence[];
  withheld: WithheldEvidence[];
  notes: NoteView[];
  truncated: boolean;
  truncation?: ExportTruncation;
}

// --- Request bodies ------------------------------------------------------------

/**
 * createIncidentRequest. The owner is the caller and the cluster is the local
 * one; neither is accepted from the body.
 */
export interface CreateIncidentRequest {
  title: string;
  summary: string;
  windowStart: string;
  windowEnd?: string;
}

/** updateIncidentRequest: an omitted field keeps its current value. */
export interface UpdateIncidentRequest {
  title?: string;
  summary?: string;
  status?: IncidentStatus;
}

/**
 * captureRequest. Decoded strictly: there is no cluster and no uid field, and
 * sending either is a 400.
 */
export interface CaptureRequest {
  namespace: string;
  kind: CaptureKind;
  name: string;
  sources?: CaptureSourceId[];
}

/** grantRequest. Decoded strictly. */
export interface GrantRequest {
  granteeId: string;
  canAnnotate: boolean;
}

// --- View-model helpers (U24b) -------------------------------------------------
//
// Pure functions the incident workspace islands share. They decide wording,
// links and ordering only; nothing here fetches or touches the DOM.

/** The kinds the capture form offers, in display order. */
export const CAPTURE_KINDS: readonly CaptureKind[] = [
  "Deployment",
  "StatefulSet",
  "DaemonSet",
  "Pod",
  "Service",
  "PersistentVolumeClaim",
];

/** The capture sources, in display order, with the label the form shows. */
export const CAPTURE_SOURCES: readonly {
  id: CaptureSourceId;
  label: string;
}[] = [
  { id: "diagnostics", label: "Diagnostic checks" },
  { id: "object", label: "Object snapshot and live link" },
  { id: "events", label: "Events" },
];

/** The label of a capture source id; an unknown id is shown as sent. */
export function captureSourceLabel(id: string): string {
  return CAPTURE_SOURCES.find((s) => s.id === id)?.label ?? id;
}

/**
 * The label of each completeness state. Five distinct states, never
 * collapsed into one "error": a forbidden source is not a failed one, and a
 * timed-out source may succeed on retry.
 */
export function completenessLabel(c: Completeness): string {
  switch (c) {
    case "complete":
      return "Complete";
    case "partial":
      return "Partial";
    case "failed":
      return "Failed";
    case "forbidden":
      return "Forbidden";
    case "timed_out":
      return "Timed out";
    default:
      return String(c);
  }
}

/** What a completeness state means for the reader, in one sentence. */
export function completenessDescription(c: Completeness): string {
  switch (c) {
    case "complete":
      return "Everything this source set out to observe was observed.";
    case "partial":
      return "Some of what this source set out to observe is missing.";
    case "failed":
      return "The source could not observe anything.";
    case "forbidden":
      return "You were not allowed to read what this source needed.";
    case "timed_out":
      return "The source did not finish in time.";
    default:
      return "Unknown completeness.";
  }
}

/** The label of an evidence kind. */
export function evidenceKindLabel(kind: EvidenceKind): string {
  switch (kind) {
    case "diagnostic_check":
      return "Diagnostic check";
    case "object_summary":
      return "Object summary";
    case "event_list":
      return "Events";
    default:
      return String(kind);
  }
}

/** Shown in place of an observation time the source did not record. */
export const OBSERVATION_TIME_UNKNOWN = "observation time unknown";

/**
 * The two timestamps of a readable item. `observed` is null when the source
 * recorded no observation time; it is NEVER filled in from `collectedAt`
 * (the two routinely differ by minutes, and a fallback would present the
 * capture time as the time the cluster saw the fact).
 */
export function evidenceTimestamps(e: Evidence): {
  observed: string | null;
  captured: string;
} {
  return { observed: e.sourceObservedAt ?? null, captured: e.collectedAt };
}

/**
 * Why an item is withheld. The text names no scope: what the item is about
 * (namespace, kind, name) is itself what is being withheld.
 */
export function withheldReasonText(reason: WithheldReason): string {
  switch (reason) {
    case "forbidden":
      return "You do not currently have access to this evidence's scope.";
    case "authorization_check_unavailable":
      return "Your access to this evidence could not be checked right now. Reload to try again.";
    default:
      return "This evidence is withheld from you.";
  }
}

/**
 * What redaction did to an item, as short phrases; empty when nothing was
 * redacted. Shown so the reader knows what they are not seeing.
 */
export function redactionNotes(r: RedactionMeta): string[] {
  const notes: string[] = [];
  if (r.applied) {
    notes.push(
      r.fieldsRemoved === 1
        ? "Redacted: 1 field removed"
        : `Redacted: ${r.fieldsRemoved} fields removed`,
    );
  }
  if (r.truncated) notes.push("Truncated to the size limit");
  if (r.secretDerived) notes.push("Derived from a Secret; values are masked");
  return notes;
}

/**
 * Sorts timeline items newest capture first (`collectedAt` descending), ties
 * broken by id so the order is stable across reloads and pages.
 */
export function sortTimeline(items: EvidenceItem[]): EvidenceItem[] {
  return [...items].sort((a, b) => {
    const ta = Date.parse(a.collectedAt);
    const tb = Date.parse(b.collectedAt);
    if (ta !== tb) return tb - ta;
    return a.id < b.id ? 1 : a.id > b.id ? -1 : 0;
  });
}

/** One evidence page's readable items and withheld placeholders as one list. */
export function pageItems(
  page: Pick<EvidencePage, "evidence" | "withheld">,
): EvidenceItem[] {
  return [...(page.evidence ?? []), ...(page.withheld ?? [])];
}

/**
 * Where a live link's object lives: the in-app detail page and the API path
 * that reads it, or null for a kind the app has no detail page for.
 */
export function liveLinkTarget(
  source: SourceRef,
): { href: string; apiPath: string } | null {
  const segment = KIND_ROUTE_MAP[source.kind];
  if (!segment || !source.namespace || !source.name) return null;
  const ns = encodeURIComponent(source.namespace);
  const name = encodeURIComponent(source.name);
  return {
    href: `/${getResourceSection(source.kind)}/${segment}/${ns}/${name}`,
    apiPath: `/v1/resources/${segment}/${ns}/${name}`,
  };
}
