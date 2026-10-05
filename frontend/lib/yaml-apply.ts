/**
 * Shared primitives for islands that wrap the `/api/v1/yaml/{validate,apply}`
 * endpoints. Two consumers as of Phase K:
 *   - `islands/YamlApplyPage.tsx` (general-purpose paste-or-upload editor)
 *   - `islands/SecretStoreFromTemplateEditor.tsx` (template-driven editor)
 *
 * Each consumer keeps its own visual treatment (the general editor shows a
 * dense multi-doc table; the template editor shows a focused single-resource
 * list); this module owns only the types + state machine common to both.
 *
 * **Target pinning (D4 / AE2).** A successful preview records the cluster it
 * ran against. With `pinApplyToPreview`, the apply is addressed to that
 * cluster — header and `targetCluster`/`targetGeneration` query both — and
 * never to whatever the operator is looking at by the time they press Apply.
 * The server refuses a pin that disagrees with its target, so a mismatch
 * cannot apply anywhere. Without the option the hook keeps its original
 * unpinned apply, which is what the template editor relies on.
 */

import {
  type ReadonlySignal,
  type Signal,
  useComputed,
  useSignal,
  useSignalEffect,
} from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { ApiError, apiPostRaw, errorExtra, errorExtraFlag } from "./api.ts";
import { currentUserId } from "./auth.ts";
import type {
  ApplyTracking,
  TrackedApplyRefusalReason,
} from "./change-types.ts";
import {
  type ClusterTarget,
  clusterEpoch,
  currentTarget,
  LOCAL_CLUSTER_ID,
  selectedCluster,
  selectedClusterGeneration,
  UNKNOWN_GENERATION,
} from "./cluster.ts";
import {
  clearStoredAttempt,
  PENDING_ATTEMPT_TTL_MS,
  type PendingAttempt,
  readStoredAttempt,
  writeStoredAttempt,
} from "./pending-apply.ts";
import { uuidv4 } from "./uuid.ts";

export interface ApplyResult {
  index: number;
  kind: string;
  name: string;
  namespace?: string;
  /**
   * "created" | "configured" | "unchanged" | "failed". Kept a bare string:
   * narrowing it to `ApplyAction` (the union receipt views use) breaks
   * existing code that builds an ApplyResult from a string, such as
   * `secretstore-template-nav_test.ts`. Narrow it when those writers do.
   */
  action: string;
  error?: string;
}

export interface ApplyResponse {
  results: ApplyResult[];
  summary: {
    total: number;
    created: number;
    configured: number;
    unchanged: number;
    failed: number;
  };
  /**
   * Present only when the request opted in with `?trackedOperationId=`.
   * Absent for every legacy caller, so the untracked contract is unchanged.
   * The shared hook does not render it; consumers that do (the change
   * receipt UI) read it from `result`.
   */
  tracking?: ApplyTracking;
}

/** One document's dry-run verdict from `/yaml/validate`. */
export interface ValidateDocument {
  index: number;
  kind: string;
  name: string;
  namespace?: string;
  valid: boolean;
  errors?: Array<{ field?: string; message: string }>;
}

/** Body of `/yaml/validate`: the verdicts plus the target they ran against. */
export interface ValidateResponse {
  documents: ValidateDocument[];
  valid: boolean;
  targetCluster: string;
  targetGeneration: string;
}

/** The target a preview ran against, which a pinned apply is held to. */
export interface YamlApplyPin {
  /** The operator's selection when the preview was issued. */
  readonly target: ClusterTarget;
  /** The server's identity for that target, echoed back on apply. */
  readonly targetCluster: string;
  readonly targetGeneration: string;
  /** Epoch milliseconds at which the preview succeeded. */
  readonly pinnedAt: number;
}

export interface UseYamlApplyOptions {
  /**
   * When set, an apply call appends `?force=true` to the apply URL. The
   * upstream `/yaml/apply` route honors this flag for SSA conflict resolution.
   */
  forceConflicts?: Signal<boolean>;
  /**
   * Called after a successful apply with the parsed response. Lets the caller
   * trigger side effects (e.g., navigate to the resulting resource's detail
   * page) without re-implementing the apply state machine.
   */
  onApplySuccess?: (res: ApplyResponse) => void;
  /**
   * Require a successful preview before applying, and address the apply to
   * the previewed cluster rather than the current selection. Off by default
   * so existing consumers keep their unpinned apply.
   */
  pinApplyToPreview?: boolean;
  /**
   * Opt in to a tracked apply. While `true`, `handleApply` sends
   * `?trackedOperationId=<uuid>` and the server records a change receipt.
   * Each user-initiated apply mints a fresh UUIDv4 via `uuidv4()` (which
   * works on an HTTP-only deployment, where `crypto.randomUUID` does not
   * exist); a retry of the SAME attempt (the request was cut off, or the
   * server answered 5xx, and the operator presses Apply again with unchanged
   * content, force, pin, repairOf and target cluster) reuses the stored id so
   * the server can replay the outcome instead of applying twice. Absent or
   * `false` leaves the request exactly as it was before tracking existed.
   *
   * An unknown-outcome id is also kept in sessionStorage, keyed by a hash of
   * the request, so a remount, reload or page navigation in the same tab
   * continues the attempt. It stays reusable for `PENDING_ATTEMPT_TTL_MS`
   * and is cleared on success or a definite refusal. Where storage is
   * unavailable only the in-memory copy exists, and a remount forgets it:
   * check the receipt list before applying again after such an interruption.
   */
  tracked?: Signal<boolean>;
  /**
   * Operation id of the receipt this apply repairs, sent as `?repairOf=`.
   * Recorded server-side as a link only; it grants nothing. Ignored unless
   * the apply is tracked.
   */
  repairOf?: Signal<string | null>;
}

export interface UseYamlApplyReturn {
  yamlContent: Signal<string>;
  applying: Signal<boolean>;
  validating: Signal<boolean>;
  error: Signal<string | null>;
  /** The last apply's outcome. Validate results go to `preview`. */
  result: Signal<ApplyResponse | null>;
  handleValidate: () => Promise<void>;
  handleApply: () => Promise<void>;
  /** The last successful preview's verdicts. */
  preview: ReadonlySignal<ValidateResponse | null>;
  /** The target the last successful preview ran against. */
  pin: ReadonlySignal<YamlApplyPin | null>;
  /**
   * The operator has since selected a different cluster than the pin. The
   * pin is unchanged: a pinned apply still goes to the previewed cluster.
   */
  pinStale: ReadonlySignal<boolean>;
  /** Forgets the preview and its pin; a pinned apply then needs a new one. */
  clearPin: () => void;
  /**
   * The operation id the last apply attempt was sent under, or null when it
   * was untracked, no apply has run, or a later Validate or Apply press
   * started over. It describes only that last attempt; the id a retry will
   * reuse is kept separately.
   */
  lastOperationId: ReadonlySignal<string | null>;
  /**
   * Whether the last tracked apply failed in a way that may still have
   * changed the cluster under `lastOperationId` (see attemptMayHaveApplied),
   * so its receipt is worth pointing at. False after any definite refusal,
   * and reset by Validate and by each Apply press.
   */
  attemptOutcomeUnknown: ReadonlySignal<boolean>;
  /**
   * The typed reason the server refused the last tracked apply (409 id
   * reasons, 503 receipt store), or null. `error` carries the same refusal as
   * operator-facing text.
   */
  trackedRefusal: ReadonlySignal<TrackedApplyRefusal | null>;
}

/** The pin parameters an apply echoes back to the server. */
export interface ApplyQueryPin {
  targetCluster: string;
  targetGeneration: string;
}

/** Everything that shapes the `/yaml/apply` query string. */
export interface ApplyQueryInput {
  force?: boolean;
  /** Release C target pin; both parameters travel together. */
  pin?: ApplyQueryPin | null;
  /** UUIDv4 idempotency key of a tracked apply. */
  trackedOperationId?: string | null;
  /** Receipt this apply repairs. Emitted only together with `trackedOperationId`. */
  repairOf?: string | null;
}

/**
 * Builds the apply query string: `""` when nothing is set, otherwise `?` plus
 * the parameters in a fixed order (force, targetCluster, targetGeneration,
 * trackedOperationId, repairOf), so equal inputs always produce equal URLs.
 * With no tracking and no `repairOf` it is byte-identical to what the hook
 * built before tracking existed.
 */
export function buildApplyQuery(input: ApplyQueryInput = {}): string {
  const query = new URLSearchParams();
  if (input.force) query.set("force", "true");
  if (input.pin) {
    query.set("targetCluster", input.pin.targetCluster);
    query.set("targetGeneration", input.pin.targetGeneration);
  }
  if (input.trackedOperationId) {
    query.set("trackedOperationId", input.trackedOperationId);
    if (input.repairOf) query.set("repairOf", input.repairOf);
  }
  return query.size > 0 ? `?${query}` : "";
}

/** A tracked apply the server refused, in terms the operator can act on. */
export interface TrackedApplyRefusal {
  reason: TrackedApplyRefusalReason;
  /** Operator-facing text. */
  message: string;
  /** For `operation_in_flight`: the receipt to poll instead of resubmitting. */
  receiptId?: string;
  /**
   * For `receipt_store_unavailable`, the server's `extra.applied`: false means
   * nothing was applied. Absent when the server did not say.
   */
  applied?: boolean;
  /**
   * For `receipt_store_unavailable`, the server's
   * `extra.retrySameOperationId`: true means a retry must keep the operation
   * id, false means the id may be spent and a new one must be minted. Absent
   * when the server did not say.
   */
  retrySameOperationId?: boolean;
}

/**
 * Maps a tracked apply's 409 id refusals and its 503 `receipt_store_unavailable`
 * to a typed refusal. Returns null for any other failure (including a pin
 * refusal, which `pinRefusalMessage` owns). Reads only `ApiError.reason` and
 * `errorExtra`; never the raw body.
 */
export function trackedApplyRefusal(err: unknown): TrackedApplyRefusal | null {
  if (!(err instanceof ApiError)) return null;
  if (err.status === 503 && err.reason === "receipt_store_unavailable") {
    const applied = errorExtraFlag(err, "applied");
    const retrySameOperationId = errorExtraFlag(err, "retrySameOperationId");
    return {
      reason: "receipt_store_unavailable",
      message:
        "The change record could not be saved, so nothing was applied. Try again shortly, or turn off change tracking to apply without a record.",
      ...(applied === undefined ? {} : { applied }),
      ...(retrySameOperationId === undefined ? {} : { retrySameOperationId }),
    };
  }
  if (err.status !== 409) return null;
  switch (err.reason) {
    case "operation_id_conflict":
      return {
        reason: "operation_id_conflict",
        message:
          "This apply could not be recorded because its operation id is already in use. Nothing was applied. Apply again to start a new change.",
      };
    case "operation_in_flight": {
      const receiptId = errorExtra(err, "receiptId");
      return {
        reason: "operation_in_flight",
        message:
          "This apply is still running. Check its change receipt instead of applying again.",
        ...(receiptId ? { receiptId } : {}),
      };
    }
    case "operation_id_reused":
      return {
        reason: "operation_id_reused",
        message:
          "This operation id was already used for different content or a different cluster. Nothing was applied by this request. Validate again and apply as a new change.",
      };
    default:
      return null;
  }
}

/**
 * Whether a failed apply left its outcome unknown, so pressing Apply again is
 * a retry of the same attempt and must reuse the operation id. True for a
 * request that got no response (network error, bad body), any 5xx, and a 409
 * `operation_in_flight` (the same attempt, still running). Every other
 * failure is a definite answer; the next apply is a new attempt.
 */
export function isIndeterminateApplyFailure(err: unknown): boolean {
  if (!(err instanceof ApiError)) return true;
  if (err.status >= 500) return true;
  return err.status === 409 && err.reason === "operation_in_flight";
}

/**
 * Whether a failed tracked apply may have changed the cluster under its
 * operation id, so its receipt is worth pointing at. True for an unknown
 * outcome (no response, a 5xx) and for a store failure that does not rule
 * it out: `applied: false` covers only this request, so with
 * `retrySameOperationId: true` an earlier send of the same id may have
 * applied. False for every definite refusal (a pin mismatch, 4xx) and for
 * `operation_in_flight`, which carries its own receipt link.
 */
export function attemptMayHaveApplied(
  err: unknown,
  refusal: TrackedApplyRefusal | null,
): boolean {
  if (refusal?.reason === "receipt_store_unavailable") {
    return refusal.applied !== false || refusal.retrySameOperationId === true;
  }
  if (refusal) return false;
  if (err instanceof ApiError && err.status === 409) return false;
  return isIndeterminateApplyFailure(err);
}

function normalizeClusterId(id: string | undefined): string {
  return id || LOCAL_CLUSTER_ID;
}

/**
 * Operator-facing text for an apply the server refused because its pin no
 * longer matches. Returns null for any other failure.
 */
function pinRefusalMessage(err: unknown): string | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null;
  if (err.reason === "cluster_pin_mismatch") {
    const pinned = errorExtra(err, "pinnedClusterId") ?? "unknown";
    const requested = errorExtra(err, "requestClusterId") ?? "unknown";
    return `Apply refused: the preview ran against cluster "${pinned}", but this request was sent to "${requested}". Nothing was applied. Validate again to apply.`;
  }
  if (err.reason === "cluster_generation_mismatch") {
    return "Apply refused: the cluster you previewed has been re-registered since the preview. Nothing was applied. Validate again to apply.";
  }
  return null;
}

/**
 * Hook that owns the full validate/apply state machine for a single YAML
 * editor instance. Caller passes the initial YAML; this hook returns signals
 * for editor binding, plus stable handlers wired to the api module.
 */
export function useYamlApply(
  initialYaml: string,
  options: UseYamlApplyOptions = {},
): UseYamlApplyReturn {
  const yamlContent = useSignal(initialYaml);
  const applying = useSignal(false);
  const validating = useSignal(false);
  const error = useSignal<string | null>(null);
  const result = useSignal<ApplyResponse | null>(null);
  const preview = useSignal<ValidateResponse | null>(null);
  const pin = useSignal<YamlApplyPin | null>(null);
  const lastOperationId = useSignal<string | null>(null);
  const attemptOutcomeUnknown = useSignal(false);
  const trackedRefusal = useSignal<TrackedApplyRefusal | null>(null);
  // The attempt a retry would continue: its id plus the request it was minted
  // for. Reused only while the request is unchanged, so an id never travels
  // with different content (the server refuses that as operation_id_reused).
  // Mirrored to sessionStorage so a remount or reload can continue it.
  const pendingAttempt = useRef<PendingAttempt | null>(null);
  const validateAbort = useRef<{
    controller: AbortController;
    issuedEpoch: number;
  } | null>(null);

  // A pin taken while the selection was still restored under the unknown
  // sentinel cannot be stale on generation alone: the id is unchanged, and a
  // re-registration always mints a new id, so learning the real generation
  // later is not a move. (The server's targetGeneration is not compared here:
  // it and the cluster list's createdAt format the same instant differently
  // unless the backend runs in UTC. The apply is enforced on it server-side.)
  const pinStale = useComputed(() => {
    const p = pin.value;
    if (!p) return false;
    if (p.target.clusterId !== selectedCluster.value) return true;
    return (
      p.target.generation !== UNKNOWN_GENERATION &&
      p.target.generation !== selectedClusterGeneration.value
    );
  });

  // A switch makes an in-flight preview describe a cluster that is no longer
  // on screen: cancel it rather than let it land late. An in-flight apply is
  // deliberately left alone — aborting the fetch cannot undo a server-side
  // apply, only hide its outcome, and that outcome is labelled with its pin.
  useSignalEffect(() => {
    const epoch = clusterEpoch.value;
    const ctl = validateAbort.current;
    if (ctl && ctl.issuedEpoch !== epoch) ctl.controller.abort();
  });
  useEffect(() => () => validateAbort.current?.controller.abort(), []);

  const clearPin = useCallback(() => {
    pin.value = null;
    preview.value = null;
  }, []);

  const handleValidate = useCallback(async () => {
    if (applying.value || validating.value) return;
    const target = currentTarget();
    const controller = new AbortController();
    const inFlight = { controller, issuedEpoch: target.epoch };
    validateAbort.current = inFlight;
    validating.value = true;
    error.value = null;
    result.value = null;
    preview.value = null;
    pin.value = null;
    trackedRefusal.value = null;
    // A new preview starts over: the last apply's outcome no longer describes
    // what is on screen. (The retry id itself lives in pendingAttempt.)
    lastOperationId.value = null;
    attemptOutcomeUnknown.value = false;
    try {
      const res = await apiPostRaw<ValidateResponse>(
        "/v1/yaml/validate",
        yamlContent.value,
        "text/yaml",
        { clusterId: target.clusterId, signal: controller.signal },
      );
      // The operator switched while this was in flight. Whatever the answer
      // says, it describes a cluster that is no longer on screen.
      if (clusterEpoch.peek() !== target.epoch) return;
      const data = res.data;
      if (
        normalizeClusterId(data.targetCluster) !==
        normalizeClusterId(target.clusterId)
      ) {
        error.value = `Validation ran against cluster "${data.targetCluster}" instead of "${target.clusterId}". Validate again.`;
        return;
      }
      preview.value = data;
      pin.value = {
        target,
        targetCluster: data.targetCluster,
        targetGeneration: data.targetGeneration,
        pinnedAt: Date.now(),
      };
    } catch (err) {
      if (controller.signal.aborted || clusterEpoch.peek() !== target.epoch) {
        return;
      }
      error.value = err instanceof Error ? err.message : "Validation failed";
    } finally {
      if (validateAbort.current === inFlight) validateAbort.current = null;
      validating.value = false;
    }
  }, []);

  const handleApply = useCallback(async () => {
    if (applying.value || validating.value) return;
    // A new press starts a new answer: nothing from the previous attempt
    // describes it, even when it is refused before it is sent.
    lastOperationId.value = null;
    attemptOutcomeUnknown.value = false;
    const pinned = pin.peek();
    if (options.pinApplyToPreview && !pinned) {
      error.value =
        "Validate first, so the apply is tied to a reviewed cluster.";
      return;
    }
    applying.value = true;
    error.value = null;
    result.value = null;
    trackedRefusal.value = null;
    // Read once: the retry key and the request body must be the same text.
    const yaml = yamlContent.value;
    // Whether this send continues an earlier attempt's id (a retry) rather
    // than minting its own.
    let reusedId = false;
    // Identifies the request for retry purposes; set once it is built.
    let requestKey: string | null = null;
    // Ends the attempt: a retry mints a new id from here on.
    const releaseAttempt = () => {
      pendingAttempt.current = null;
      if (requestKey !== null) clearStoredAttempt(requestKey);
    };
    try {
      const baseQuery: ApplyQueryInput = {
        force: Boolean(options.forceConflicts?.value),
        pin:
          options.pinApplyToPreview && pinned
            ? {
                targetCluster: pinned.targetCluster,
                targetGeneration: pinned.targetGeneration,
              }
            : null,
        repairOf: options.repairOf?.value ?? null,
      };
      // The cluster is decided once, here: it keys the retry and is the
      // X-Cluster-ID, so the two cannot diverge.
      const clusterId =
        options.pinApplyToPreview && pinned
          ? pinned.target.clusterId
          : selectedCluster.peek();
      // Every input the request carries, and who is sending it, so an id
      // never travels to a different identity. repairOf is listed explicitly
      // because buildApplyQuery only emits it beside a tracked id.
      requestKey = JSON.stringify([
        baseQuery.force,
        baseQuery.pin,
        baseQuery.repairOf,
        clusterId,
        currentUserId(),
        yaml,
      ]);
      let trackedOperationId: string | null = null;
      if (options.tracked?.value) {
        const now = Date.now();
        const mem = pendingAttempt.current;
        const pending =
          mem !== null &&
          mem.requestKey === requestKey &&
          now - mem.at < PENDING_ATTEMPT_TTL_MS
            ? mem
            : readStoredAttempt(requestKey, now);
        reusedId = pending !== null;
        const attempt: PendingAttempt = pending ?? {
          id: uuidv4(),
          requestKey,
          at: now,
        };
        trackedOperationId = attempt.id;
        pendingAttempt.current = attempt;
        writeStoredAttempt(attempt);
      } else {
        // Untracked: any unknown-outcome tracked attempt for this request is
        // superseded.
        releaseAttempt();
      }
      lastOperationId.value = trackedOperationId;
      const queryStr = buildApplyQuery({ ...baseQuery, trackedOperationId });
      const res = await apiPostRaw<ApplyResponse>(
        `/v1/yaml/apply${queryStr}`,
        yaml,
        "text/yaml",
        { clusterId },
      );
      releaseAttempt();
      result.value = res.data;
      options.onApplySuccess?.(res.data);
    } catch (err) {
      const refusal = pinRefusalMessage(err);
      const trackedFailure = trackedApplyRefusal(err);
      // A definite answer ends the attempt; only an unknown outcome keeps its
      // id for the retry. A 503 receipt_store_unavailable says whether the id
      // may be reused: the server's `retrySameOperationId` decides when it is
      // present (true: the record insert or a receipt read failed, so the id
      // is kept and a retry continues the same operation; false: marking the
      // receipt failed or no receipt store is configured, the id may be
      // spent, so mint a new one). In every case `applied: false` speaks only
      // for THIS request. Without the flag, a 503 on a FRESH id is definite
      // (no earlier send exists, so a retry would only replay a failed
      // receipt or report it in flight), while on a REUSED id the id stays.
      let releaseStoreFailure = false;
      if (trackedFailure?.reason === "receipt_store_unavailable") {
        releaseStoreFailure =
          trackedFailure.retrySameOperationId !== undefined
            ? !trackedFailure.retrySameOperationId
            : !reusedId;
      }
      if (!isIndeterminateApplyFailure(err) || releaseStoreFailure) {
        releaseAttempt();
      }
      attemptOutcomeUnknown.value =
        lastOperationId.peek() !== null &&
        attemptMayHaveApplied(err, trackedFailure);
      if (trackedFailure) {
        trackedRefusal.value = trackedFailure;
        error.value = trackedFailure.message;
      } else if (refusal) {
        // A refused pin can never succeed on retry. Drop it so the only way
        // forward is a fresh preview against the cluster actually targeted.
        pin.value = null;
        preview.value = null;
        error.value = refusal;
      } else {
        error.value = err instanceof Error ? err.message : "Apply failed";
      }
    } finally {
      applying.value = false;
    }
  }, []);

  return {
    yamlContent,
    applying,
    validating,
    error,
    result,
    handleValidate,
    handleApply,
    preview,
    pin,
    pinStale,
    clearPin,
    lastOperationId,
    attemptOutcomeUnknown,
    trackedRefusal,
  };
}
