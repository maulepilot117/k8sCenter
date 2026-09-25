/**
 * Pure helpers for the ESO evidence panel (Release B, U16). No fetching and no
 * signals here: the panel island owns I/O, and everything it decides from a
 * response is a function in this file so it can be tested without a DOM.
 */

import { ApiError } from "@/lib/api.ts";
import type {
  ClusterExternalSecret,
  EvidenceKind,
  EvidenceProjection,
  EvidenceUnavailableReason,
  HistoryEntry,
} from "@/lib/eso-types.ts";

/** Which history, if any, an evidence panel may show for a kind. */
export type EvidenceHistorySupport = "real" | "unsupported" | "children";

/**
 * The evidence support matrix (plan D5) as data, so a kind cannot acquire a
 * tab by accident. The rule it encodes: a tab exists only where a collector
 * for that kind exists. Sync history is collected per ExternalSecret, so an
 * ExternalSecret's attempts are never shown under a store, and a
 * ClusterExternalSecret links to its generated children instead of claiming
 * their attempts as its own (R12).
 */
export const EVIDENCE_SUPPORT: Readonly<
  Record<
    EvidenceKind,
    Readonly<{
      yaml: boolean;
      events: boolean;
      history: EvidenceHistorySupport;
    }>
  >
> = Object.freeze({
  externalsecrets: { yaml: true, events: true, history: "real" },
  clusterexternalsecrets: { yaml: true, events: true, history: "children" },
  secretstores: { yaml: true, events: true, history: "unsupported" },
  clustersecretstores: { yaml: true, events: true, history: "unsupported" },
  pushsecrets: { yaml: true, events: true, history: "unsupported" },
});

export type EvidenceTabKey = "yaml" | "events" | "history" | "generated";

export interface EvidenceTab {
  key: EvidenceTabKey;
  label: string;
}

/** The evidence tabs for a kind, in display order, derived from the matrix. */
export function evidenceTabsFor(kind: EvidenceKind): EvidenceTab[] {
  const support = EVIDENCE_SUPPORT[kind];
  const tabs: EvidenceTab[] = [];
  if (support.yaml) tabs.push({ key: "yaml", label: "YAML" });
  if (support.events) tabs.push({ key: "events", label: "Events" });
  if (support.history === "real") {
    tabs.push({ key: "history", label: "History" });
  } else if (support.history === "children") {
    tabs.push({ key: "generated", label: "Generated ExternalSecrets" });
  }
  return tabs;
}

/**
 * Maps a failed evidence request to the reason the panel explains. Every
 * failure the U14a/U15 endpoints can return keeps its own meaning; only an
 * error the server did not name falls back to "error". An unknown 503 reason
 * is also "error": guessing it means "not configured" would misstate an
 * outage.
 */
export function classifyEvidenceError(err: unknown): EvidenceUnavailableReason {
  if (!(err instanceof ApiError)) return "error";
  switch (err.status) {
    case 403:
      return "forbidden";
    case 404:
      return err.reason === "evidence_kind_not_served"
        ? "unsupported_kind"
        : "not_found";
    case 409:
      // The YAML export refuses a same-name replacement (expectUID).
      return err.reason === "uid_mismatch" ? "replaced" : "error";
    case 501:
      return "remote_unsupported";
    case 503:
      switch (err.reason) {
        case "eso_not_detected":
        case "history_unavailable":
        case "discovery_unavailable":
          return err.reason;
        default:
          return "error";
      }
    default:
      return "error";
  }
}

const UNAVAILABLE_MESSAGES: Readonly<
  Record<EvidenceUnavailableReason, string>
> = Object.freeze({
  remote_unsupported:
    "This evidence is recorded for the local cluster only. Switch to the local cluster to view it.",
  eso_not_detected:
    "External Secrets Operator is not detected on this cluster.",
  history_unavailable:
    "Sync history is temporarily unavailable. This is not the same as having no history; try again shortly.",
  discovery_unavailable:
    "The cluster's API discovery is temporarily unavailable, so this resource could not be resolved. Try again shortly.",
  forbidden: "You do not have permission to view this evidence.",
  not_found: "This resource no longer exists.",
  unsupported_kind: "This cluster does not serve this kind of resource.",
  replaced:
    "This resource was deleted and recreated under the same name. Reload the page to see the current object's evidence.",
  error: "This evidence could not be loaded.",
});

/** The explanation a tab shows in place of evidence it could not load. */
export function unavailableMessage(reason: EvidenceUnavailableReason): string {
  return UNAVAILABLE_MESSAGES[reason];
}

function newestFirst(a: HistoryEntry, b: HistoryEntry): number {
  const byTime = Date.parse(b.attemptAt) - Date.parse(a.attemptAt);
  return byTime !== 0 ? byTime : b.id - a.id;
}

/**
 * Appends a history page to the rows already shown. Rows are keyed by id, so
 * an entry repeated across a page boundary appears once, and the result is
 * kept newest first — the server's (attemptAt, id) keyset order — even if a
 * page arrives out of order. Inputs are not mutated.
 */
export function mergeHistoryPages(
  prev: readonly HistoryEntry[],
  next: readonly HistoryEntry[],
): HistoryEntry[] {
  const seen = new Set(prev.map((e) => e.id));
  const merged = [...prev];
  for (const e of next) {
    if (!seen.has(e.id)) {
      seen.add(e.id);
      merged.push(e);
    }
  }
  return merged.sort(newestFirst);
}

/**
 * Reduces history rows to what `projection` allows. When Secret read is
 * revoked between pages, the newest page arrives outcome-only while earlier
 * rows still carry messages and key names; the whole list is shown under the
 * newest projection, so those rows are stripped to match it rather than kept
 * visible under an "outcome-only" note. Inputs are not mutated.
 */
export function redactEntriesTo(
  entries: readonly HistoryEntry[],
  projection: EvidenceProjection,
): HistoryEntry[] {
  if (projection.level === "full") return [...entries];
  return entries.map(({ id, attemptAt, outcome, reason, diffKeyCounts }) => ({
    id,
    attemptAt,
    outcome,
    reason,
    diffKeyCounts,
  }));
}

/**
 * True when a response must be discarded: it describes an object other than
 * the one the panel shows now, or cannot say which object it describes. A
 * delete-and-recreate keeps the name but changes the UID, so a late response
 * for the old object must not be rendered as the new one's evidence (R1).
 */
export function isStaleResponse(
  currentUid: string,
  responseUid: string | undefined,
): boolean {
  return !responseUid || responseUid !== currentUid;
}

/**
 * The note shown beside a redacted response, or null when nothing is hidden.
 * It names the grant so the reader knows the gap is permission, not absence.
 */
export function describeProjection(p: EvidenceProjection): string | null {
  if (p.level === "full") return null;
  return "Some details are hidden — viewing messages and Secret key names requires Secret read access in this namespace.";
}

/** A path segment for an evidence URL; cluster scope is addressed as "_". */
export function evidenceNamespaceSegment(namespace: string | null): string {
  return namespace === null ? "_" : encodeURIComponent(namespace);
}

export interface GeneratedExternalSecret {
  namespace: string;
  name: string;
  /** False when the CES reports this namespace as failed. */
  provisioned: boolean;
  href: string;
}

/**
 * The ExternalSecrets a ClusterExternalSecret generated, each linking to its
 * own detail page where its own history lives. The child name is the CES's
 * `externalSecretName`, which ESO defaults to the CES name.
 */
export function generatedExternalSecrets(
  ces: Pick<
    ClusterExternalSecret,
    | "name"
    | "externalSecretBaseName"
    | "provisionedNamespaces"
    | "failedNamespaces"
  >,
): GeneratedExternalSecret[] {
  const name = ces.externalSecretBaseName || ces.name;
  const child = (namespace: string, provisioned: boolean) => ({
    namespace,
    name,
    provisioned,
    href: `/external-secrets/external-secrets/${encodeURIComponent(
      namespace,
    )}/${encodeURIComponent(name)}`,
  });
  return [
    ...(ces.provisionedNamespaces ?? []).map((ns) => child(ns, true)),
    ...(ces.failedNamespaces ?? []).map((ns) => child(ns, false)),
  ];
}
