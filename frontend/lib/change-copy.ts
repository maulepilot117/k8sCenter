/**
 * Operator-facing copy and pure decisions for tracked apply and change
 * receipts (Release E U31). Every function here is a total mapping from a wire
 * value to what the UI says or allows, so the wording rules are testable
 * without mounting anything:
 *
 *   - Execution and verification are separate facts and get separate badges.
 *     Nothing here merges them into one "success".
 *   - `inconclusive` and an `unknown` outcome are never worded or toned as
 *     success.
 *   - Ownership copy is driven only by `confidence` and `reason`. Hints never
 *     read as ownership, a failed check never reads as "not managed", and no
 *     copy offers a Git write (`writableGitSource` is always false in
 *     Release E; nothing here consults it to offer one).
 *   - Repair never reuses stored content; there is none.
 *
 * Reason codes are open sets (a newer server may add one), so every reason
 * mapping falls through to a neutral rendering.
 */

import { parseAllDocuments } from "yaml";
import { ApiError } from "./api.ts";
import type {
  CheckReason,
  CheckStatus,
  CheckView,
  GitOpsTool,
  OwnedByApp,
  OwnershipObjectRef,
  OwnershipResult,
  OwnershipView,
  ReceiptDetail,
  ReceiptState,
  VerificationState,
} from "./change-types.ts";
import { LOCAL_CLUSTER_ID } from "./cluster.ts";
import type { TrackedApplyRefusal, ValidateDocument } from "./yaml-apply.ts";

// --- Shared sentences -----------------------------------------------------

/**
 * Why change records cannot be read or written: GET/POST under /v1/changes
 * answered 503. One sentence everywhere, so the cause reads the same on
 * YAML Apply, the receipt list and a receipt.
 */
export const RECORDS_UNAVAILABLE =
  "Change records are unavailable: the server has no database configured, or it cannot be reached.";

/** GET /v1/changes answered 404: this server has no change-records routes. */
export const RECORDS_UNSUPPORTED = "This server does not keep change records.";

/** The display name of a receipt's cluster; remote ids are opaque, shown as-is. */
export function clusterDisplayName(clusterId: string): string {
  return clusterId === LOCAL_CLUSTER_ID ? "Local cluster" : clusterId;
}

/** What a redaction count hides; each reads as one sentence. */
export type HiddenKind = "objects" | "checks" | "ownership" | "apps";

/**
 * The sentence for `n` items withheld because the reader no longer has
 * access to them. Empty for `n <= 0`.
 */
export function hiddenText(kind: HiddenKind, n: number): string {
  if (n <= 0) return "";
  const one = n === 1;
  switch (kind) {
    case "objects":
      return `${n} object${one ? " is" : "s are"} hidden because you no longer have access to ${one ? "it" : "them"}.`;
    case "checks":
      return `Details of ${n} check${one ? " are" : "s are"} hidden because you no longer have access to the objects they read.`;
    case "ownership":
      return `Ownership of ${n} object${one ? " is" : "s is"} hidden because you no longer have access to ${one ? "it" : "them"}.`;
    case "apps":
      return `${n} more claiming application${one ? " is" : "s are"} hidden because you no longer have access to ${one ? "it" : "them"}.`;
  }
}

/**
 * The warning above the editor when GitOps controllers manage previewed
 * objects, or null when none is confirmed or contested.
 */
export function managedSummary(count: number): string | null {
  if (count <= 0) return null;
  return count === 1
    ? "1 of these objects is managed by a GitOps controller. Applying here changes the live object; the controller may revert it on its next sync. See GitOps ownership below the editor."
    : `${count} of these objects are managed by a GitOps controller. Applying here changes the live objects; their controllers may revert them on their next sync. See GitOps ownership below the editor.`;
}

/**
 * Operator text for a refused tracked apply. A 503 `receipt_store_unavailable`
 * only says nothing was applied when the server confirmed it
 * (`applied: false`); otherwise it says the outcome is unconfirmed.
 */
export function trackedRefusalText(refusal: TrackedApplyRefusal): string {
  if (refusal.reason !== "receipt_store_unavailable") return refusal.message;
  if (refusal.applied === false) {
    // applied:false speaks only for THIS request. retrySameOperationId is
    // true when the record insert or a receipt read failed: the next Apply
    // reuses this operation id, and an earlier send under it may already
    // have applied. It is false when marking failed or recording is not
    // configured at all: the next Apply is a new change.
    const next =
      refusal.retrySameOperationId === true
        ? " An earlier attempt with this operation id may already have applied; check its change receipt. Apply again to retry this same change; it will not be applied twice."
        : refusal.retrySameOperationId === false
          ? " The next apply starts a new change. Try again shortly, or turn off change tracking to apply without a record."
          : " Try again shortly, or turn off change tracking to apply without a record.";
    return `The change record could not be saved. Nothing was applied by this request.${next}`;
  }
  return "The change record could not be saved, and the server did not confirm whether anything was applied. Check the live objects before applying again.";
}

/** Badge tone; matches components/ui/glass/StatusBadge `Tone`. */
export type ChangeTone = "ok" | "warn" | "crit" | "info" | "neutral";

export interface BadgeCopy {
  label: string;
  tone: ChangeTone;
}

// --- Execution and verification -------------------------------------------

const EXECUTION: Record<ReceiptState, BadgeCopy> = {
  previewed: { label: "Not applied", tone: "neutral" },
  applying: { label: "Applying", tone: "info" },
  applied: { label: "Applied", tone: "ok" },
  partial: { label: "Partially applied", tone: "warn" },
  failed: { label: "Failed", tone: "crit" },
  unknown: { label: "Outcome unknown", tone: "warn" },
};

/** The execution-state badge. Says what the API server accepted, nothing more. */
export function executionBadge(state: ReceiptState | string): BadgeCopy {
  return EXECUTION[state as ReceiptState] ?? { label: state, tone: "neutral" };
}

const VERIFICATION: Record<VerificationState, BadgeCopy> = {
  pending: { label: "Verification pending", tone: "neutral" },
  verifying: { label: "Verifying", tone: "info" },
  verified: { label: "Verified", tone: "ok" },
  // Neutral, never green: an inconclusive check proves nothing either way.
  inconclusive: { label: "Verification inconclusive", tone: "neutral" },
  verification_failed: { label: "Verification failed", tone: "crit" },
};

/** The verification-state badge, independent of the execution badge. */
export function verificationBadge(
  state: VerificationState | string,
): BadgeCopy {
  return (
    VERIFICATION[state as VerificationState] ?? {
      label: `Verification ${state}`,
      tone: "neutral",
    }
  );
}

/** Verification states that will not change on another poll. */
export function isFinalVerification(
  state: VerificationState | string,
): boolean {
  return (
    state === "verified" ||
    state === "inconclusive" ||
    state === "verification_failed"
  );
}

/**
 * One sentence explaining an execution state that needs one, or null. The
 * `unknown` copy is deliberately action-free: the cluster may hold some of
 * the objects, so retrying blind could apply twice.
 */
export function executionExplanation(
  state: ReceiptState | string,
): string | null {
  switch (state) {
    case "unknown":
      return "k8sCenter lost track of this apply before every outcome was recorded. Some objects may have changed on the cluster and others may not. Check the live objects before you apply anything again; there is no one-click retry for an unknown outcome.";
    case "applying":
      return "This apply had not finished when the receipt was read. Its outcome is recorded as each document lands.";
    case "partial":
      return "Some objects changed and some did not. Nothing was rolled back.";
    case "failed":
      return "No object was changed successfully.";
    case "previewed":
      return "This change was recorded but never applied.";
    default:
      return null;
  }
}

/** Default poll interval while verification is in progress (plan D6). */
export const DEFAULT_VERIFICATION_POLL_SECONDS = 5;

/** Milliseconds until the next verification poll, honouring `retryAfterSeconds`. */
export function verificationPollDelayMs(
  retryAfterSeconds?: number,
  msPerSecond = 1000,
): number {
  const s =
    retryAfterSeconds && retryAfterSeconds > 0
      ? retryAfterSeconds
      : DEFAULT_VERIFICATION_POLL_SECONDS;
  return s * msPerSecond;
}

// --- Checks -----------------------------------------------------------------

const CHECK_REASON_TEXT: Record<string, string> = {
  ok: "The postcondition held.",
  finding: "The postcondition did not hold.",
  rollout_in_progress: "The rollout has not finished yet.",
  kind_not_supported:
    "This kind has no supported readiness postcondition, so k8sCenter cannot verify it.",
  not_found: "The object no longer exists.",
  target_recreated:
    "The object was deleted and recreated after this apply, so this receipt's evidence does not carry over to it.",
  read_forbidden: "You cannot read this object, so it could not be verified.",
  window_expired:
    "The object did not reach its postcondition within the verification window.",
  identity_unknown:
    "The object's identity was not recorded, so it could not be verified.",
  outcome_unrecorded:
    "This object's apply outcome was not recorded, so it was not verified.",
  strategy_not_supported:
    "This object's update strategy cannot be observed, so its rollout could not be verified.",
  permission_denied:
    "Permission was denied while reading this object, so it could not be verified.",
  source_unavailable:
    "The cluster could not be read, so this was not verified.",
  timed_out: "Reading the object timed out, so this was not verified.",
  internal_error: "Verification hit an internal error.",
};

/** Operator-facing text for a check reason code; unknown codes are shown verbatim. */
export function checkReasonText(reason: CheckReason): string {
  return CHECK_REASON_TEXT[reason] ?? `Reason: ${reason}`;
}

const CHECK_STATUS: Record<CheckStatus, BadgeCopy> = {
  pass: { label: "Pass", tone: "ok" },
  warn: { label: "Warning", tone: "warn" },
  fail: { label: "Fail", tone: "crit" },
  inconclusive: { label: "Inconclusive", tone: "neutral" },
};

export function checkStatusBadge(status: CheckStatus | string): BadgeCopy {
  return (
    CHECK_STATUS[status as CheckStatus] ?? { label: status, tone: "neutral" }
  );
}

/**
 * The distinct reasons behind an inconclusive or failed verification, as
 * operator text, so the receipt can spell out *why* rather than show a bare
 * state. Includes redacted checks: their reason is still disclosed.
 */
export function verificationReasons(checks: CheckView[]): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const c of checks) {
    if (c.status === "pass" || seen.has(c.reason)) continue;
    seen.add(c.reason);
    out.push(checkReasonText(c.reason));
  }
  return out;
}

// --- Ownership --------------------------------------------------------------

export function toolLabel(tool: GitOpsTool | string): string {
  switch (tool) {
    case "argocd":
      return "Argo CD";
    case "fluxcd":
      return "Flux CD";
    default:
      return "GitOps";
  }
}

function appList(apps: OwnedByApp[] | undefined): string {
  return (apps ?? [])
    .map((a) => `${toolLabel(a.tool)} application ${a.appId}`)
    .join(" and ");
}

export interface OwnershipCopy {
  /** Visual tone. Warnings only where a controller is confirmed to act. */
  tone: ChangeTone;
  /** Short state word for the row's badge. */
  label: string;
  /** The full sentence. */
  text: string;
}

/**
 * What the ownership strip says for one object. Driven only by `confidence`
 * and `reason`: a hint never reads as ownership, and a check that could not
 * run never reads as "not managed".
 */
export function ownershipCopy(r: OwnershipView): OwnershipCopy {
  const suspended = (r.apps ?? []).filter((a) => a.suspended);
  const suspendedNote =
    suspended.length > 0
      ? ` ${suspended.map((a) => a.appId).join(" and ")} ${
          suspended.length === 1 ? "is" : "are"
        } suspended, so ${
          suspended.length === 1 ? "it does" : "they do"
        } not reconcile until resumed.`
      : "";
  const hiddenApps = hiddenText("apps", r.redactedApps ?? 0);
  const hidden = hiddenApps ? ` ${hiddenApps}` : "";

  switch (r.confidence) {
    case "confirmed": {
      const apps = appList(r.apps);
      return {
        tone: "warn",
        label: "Managed",
        text: `Managed by ${apps || `${toolLabel(r.controller)} (application hidden)`}. Applying here changes the live object; the controller may revert it on its next sync.${suspendedNote}${hidden}`,
      };
    }
    case "conflicting":
      return {
        tone: "warn",
        label: "Conflicting",
        text: `Two controllers claim this object${
          r.apps?.length ? `: ${appList(r.apps)}` : ""
        }. Each may overwrite the other, and either may revert your change.${suspendedNote}${hidden}`,
      };
    case "forbidden":
      if (r.reason === "apps-redacted") {
        return {
          tone: "neutral",
          label: "Hidden",
          text: "A GitOps application claimed this object, but you no longer have access to it, so its details are hidden.",
        };
      }
      return {
        tone: "neutral",
        label: "Not checked",
        text: `You cannot list ${
          r.reason === "flux-list-forbidden"
            ? "Flux CD"
            : r.reason === "argo-list-forbidden"
              ? "Argo CD"
              : "GitOps"
        } applications there, so ownership could not be determined.`,
      };
    case "unavailable":
      if (r.reason === "no-controller-installed") {
        return {
          tone: "neutral",
          label: "No controller",
          text: "No GitOps controller (Argo CD or Flux CD) is installed on this cluster.",
        };
      }
      return {
        tone: "neutral",
        label: "Not checked",
        text: `${
          r.reason === "flux-unavailable"
            ? "Flux CD"
            : r.reason === "argo-unavailable"
              ? "Argo CD"
              : "The GitOps controller"
        } did not respond, so ownership could not be checked.`,
      };
    default:
      return unknownOwnershipCopy(r.reason);
  }
}

function unknownOwnershipCopy(reason: string): OwnershipCopy {
  const unknown = (text: string): OwnershipCopy => ({
    tone: "neutral",
    label: "Unknown",
    text,
  });
  switch (reason) {
    case "hints-only":
      return unknown(
        "This object carries GitOps labels or annotations, but no controller's inventory confirms it. Ownership is unknown.",
      );
    case "partial-visibility":
      return unknown(
        "Some GitOps applications on this cluster are not visible to you, and none you can see manages this object. Ownership is unknown.",
      );
    case "search-bound-exhausted":
      return unknown(
        "k8sCenter checked as many applications as one request allows without finding this object. Ownership is unknown.",
      );
    case "argo-destination-unverified":
      return unknown(
        "An Argo CD application lists this object, but its destination could not be confirmed as this cluster. Ownership is unknown.",
      );
    case "flux-remote-kubeconfig":
      return unknown(
        "A Flux Kustomization lists this object but deploys through a remote kubeconfig, so it could not be confirmed as this cluster. Ownership is unknown.",
      );
    case "flux-helmrelease-no-inventory":
      return unknown(
        "This object points at a Flux HelmRelease, which keeps no per-object inventory k8sCenter can check. Ownership is unknown.",
      );
    case OWNERSHIP_REASON_UNREADABLE:
      return {
        tone: "neutral",
        label: "Not checked",
        text: "k8sCenter could not read this document's API group, so its ownership was not checked. Ownership is unknown.",
      };
    case "no-evidence":
      return {
        tone: "neutral",
        label: "Not managed",
        text: "No Argo CD or Flux CD application you can see claims this object.",
      };
    default:
      return unknown(`Ownership is unknown (${reason}).`);
  }
}

/**
 * Why the ownership request itself failed, worded so a failed check can never
 * be read as "not managed".
 */
export function ownershipFailureText(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.status) {
      case 404:
        return "This server does not report GitOps ownership, so it was not checked.";
      case 503:
        return "GitOps ownership could not be checked: change records are unavailable on this server.";
      case 403:
        return "You cannot check GitOps ownership on this cluster.";
      case 504:
        return "Checking GitOps ownership timed out, so it is unknown.";
    }
  }
  return "GitOps ownership could not be checked, so it is unknown.";
}

/**
 * Reason of a row the client could not send for resolution because the
 * document's API group could not be read. Client-only; never on the wire.
 */
export const OWNERSHIP_REASON_UNREADABLE = "api-version-unreadable";

/** One previewed object: resolved by the server, or not checked at all. */
export type OwnershipSlot =
  | { ref: number }
  | { unreadable: OwnershipObjectRef };

/** What to ask the server, and where each answer goes on screen. */
export interface OwnershipPlan {
  /** Refs to send, in request order. */
  refs: OwnershipObjectRef[];
  /** One slot per previewed object with an identity, in document order. */
  slots: OwnershipSlot[];
}

/**
 * Plans the ownership request for a successful preview.
 *
 * `/yaml/validate` reports kind, name and namespace per document but not the
 * API group, which ownership matching needs: Argo and Flux both key on
 * group/kind/namespace/name, and the resolver reads a missing group as the
 * core group. The group and version come from parsing the same YAML the
 * server just validated, one document at a time and aligned by index the way
 * the server indexes (empty documents skipped). A document whose apiVersion
 * cannot be read, or whose kind and name disagree with the server's, is NOT
 * sent group-less (that would be checked as a core-group object and could
 * read as unmanaged); it becomes an `unreadable` slot shown as unknown.
 * Only that document is affected. A missing namespace is left for the server
 * to default by the kind's scope, as the apply does. Documents without a
 * name (generateName) are skipped: they have no identity to resolve yet.
 */
export function ownershipPlanFromPreview(
  documents: ValidateDocument[],
  yamlText: string,
): OwnershipPlan {
  const parsed = parsedApiVersions(yamlText);
  const plan: OwnershipPlan = { refs: [], slots: [] };
  for (const d of documents) {
    if (!d.kind || !d.name) continue;
    const ref: OwnershipObjectRef = { kind: d.kind, name: d.name };
    if (d.namespace) ref.namespace = d.namespace;
    const p = parsed[d.index];
    if (!p || p.kind !== d.kind || p.name !== d.name || !p.apiVersion) {
      plan.slots.push({ unreadable: ref });
      continue;
    }
    const slash = p.apiVersion.indexOf("/");
    if (slash >= 0) {
      ref.group = p.apiVersion.slice(0, slash);
      ref.version = p.apiVersion.slice(slash + 1);
    } else {
      ref.version = p.apiVersion;
    }
    plan.slots.push({ ref: plan.refs.length });
    plan.refs.push(ref);
  }
  return plan;
}

/** The row shown for an object whose ownership was not checked (see the plan). */
export function unreadableOwnership(
  object: OwnershipObjectRef,
  clusterId: string,
): OwnershipView {
  return {
    object: { clusterId, ...object },
    controller: "none",
    confidence: "unknown",
    reason: OWNERSHIP_REASON_UNREADABLE,
    identityBasis: "group-kind-namespace-name",
    uidConfirmed: false,
    writableGitSource: false,
    observedAt: new Date().toISOString(),
  };
}

/**
 * The rows to show, in document order: the server's answer for each sent ref
 * (results are in request order), and a "not checked" row for each
 * unreadable document. Refs beyond `max` were not sent and are counted in
 * `omitted`.
 */
export function assembleOwnership(
  plan: OwnershipPlan,
  results: OwnershipResult[],
  max: number,
  clusterId: string,
): { results: OwnershipResult[]; omitted: number } {
  const rows: OwnershipResult[] = [];
  let omitted = 0;
  for (const slot of plan.slots) {
    if ("unreadable" in slot) {
      rows.push(unreadableOwnership(slot.unreadable, clusterId));
    } else if (slot.ref >= max) {
      omitted++;
    } else if (results[slot.ref]) {
      rows.push(results[slot.ref]);
    }
  }
  return { results: rows, omitted };
}

interface ParsedHead {
  apiVersion?: string;
  kind?: string;
  name?: string;
}

/**
 * The apiVersion, kind and name of each non-empty document, by the server's
 * document index. A document the browser's parser rejects keeps its index as
 * an empty entry, so one bad document cannot shift or strip the others.
 * Duplicate keys are accepted last-wins, as the server's decoder does.
 */
function parsedApiVersions(yamlText: string): ParsedHead[] {
  let docs: ReturnType<typeof parseAllDocuments>;
  try {
    docs = parseAllDocuments(yamlText, { uniqueKeys: false });
  } catch {
    return [];
  }
  const out: ParsedHead[] = [];
  for (const doc of docs) {
    if (doc.errors.length > 0) {
      out.push({});
      continue;
    }
    let v: unknown;
    try {
      v = doc.toJS();
    } catch {
      out.push({});
      continue;
    }
    if (!v || typeof v !== "object" || Object.keys(v).length === 0) continue;
    const o = v as Record<string, unknown>;
    const meta = o.metadata as Record<string, unknown> | undefined;
    out.push({
      apiVersion: typeof o.apiVersion === "string" ? o.apiVersion : undefined,
      kind: typeof o.kind === "string" ? o.kind : undefined,
      name: typeof meta?.name === "string" ? meta.name : undefined,
    });
  }
  return out;
}

// --- Tracking availability ---------------------------------------------------

/** Whether this server can keep change records, from a probe of `GET /v1/changes`. */
export type TrackingAvailability =
  | { status: "checking" }
  | { status: "available" }
  | { status: "unsupported"; reason: string }
  | { status: "unavailable"; reason: string }
  | { status: "unconfirmed"; reason: string };

/** Classifies a failed `GET /v1/changes` probe. */
export function trackingAvailabilityFromError(
  err: unknown,
): TrackingAvailability {
  if (err instanceof ApiError) {
    if (err.status === 404) {
      return {
        status: "unsupported",
        reason: RECORDS_UNSUPPORTED,
      };
    }
    if (err.status === 503) {
      return { status: "unavailable", reason: RECORDS_UNAVAILABLE };
    }
  }
  return {
    status: "unconfirmed",
    reason:
      "Could not confirm that change records are available. Turn this on to try; the apply is refused, with nothing applied, if the record cannot be saved.",
  };
}

// --- Repair -------------------------------------------------------------------

export type RepairEligibility =
  | { kind: "allowed" }
  | { kind: "secret" }
  | { kind: "not-applicable" };

/**
 * Whether the receipt offers "Retry failed objects". Only a `partial` or
 * `failed` receipt has failed objects to retry, and a Secret-bearing one never
 * offers it: its content is not stored and the UI must not imply it can be
 * reused.
 */
export function repairEligibility(
  receipt: Pick<ReceiptDetail, "state" | "containsSecret">,
): RepairEligibility {
  if (receipt.state !== "partial" && receipt.state !== "failed") {
    return { kind: "not-applicable" };
  }
  return receipt.containsSecret ? { kind: "secret" } : { kind: "allowed" };
}

const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

/** Whether `s` is a canonical UUIDv4, the only operation id form the server accepts. */
export function isOperationId(s: string | null | undefined): s is string {
  return typeof s === "string" && UUID_V4.test(s);
}

/** The YAML Apply URL that starts a repair of `operationId`. Carries no content. */
export function repairHref(operationId: string, clusterId?: string): string {
  const q = new URLSearchParams({ repairOf: operationId });
  if (clusterId) q.set("cluster", clusterId);
  return `/tools/yaml-apply?${q}`;
}

/** Whether two cluster ids name the same cluster (an empty id is the local one). */
export function sameCluster(a: string, b: string): boolean {
  return (a || LOCAL_CLUSTER_ID) === (b || LOCAL_CLUSTER_ID);
}

/** The web route of one receipt. */
export function receiptHref(operationId: string): string {
  return `/changes/${encodeURIComponent(operationId)}`;
}

/** First 8 characters of an operation id, for compact display. */
export function shortId(operationId: string): string {
  return operationId.slice(0, 8);
}
