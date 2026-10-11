import { ApiError } from "@/lib/api.ts";
import type { PollFailure } from "@/lib/hooks/use-poll.ts";

/**
 * Maps a failed networking read or write to what the island shows (#608
 * KTD5, KTD14). States come from the response's stable `reason` and status,
 * never its message text, so "not permitted", "cannot be read" and "reload"
 * stay distinct whatever the server's wording.
 */

export type PanelStateKind =
  | "forbidden"
  | "unavailable"
  | "reload"
  | "conflict"
  | "outcome_unknown"
  | "busy";

/** The control an island offers to recover from a state, if any. */
export type RecoveryAction = "retry" | "reload" | null;

export interface PanelState {
  kind: PanelStateKind;
  message: string;
  action: RecoveryAction;
}

/** Reasons that mean the page targets a cluster registration that changed. */
const RELOAD_REASONS = new Set([
  "cluster_unknown",
  "cluster_pin_required",
  "cluster_pin_mismatch",
  "cluster_generation_mismatch",
]);

/** The status and reason of an error, or null when it carries none. */
export function failureOf(err: unknown): PollFailure | null {
  return err instanceof ApiError
    ? { status: err.status, reason: err.reason }
    : null;
}

/**
 * The state for a failure reading or writing `what` (e.g. "BGP status").
 * A refusal offers no retry: retrying cannot change the answer (#608 R5).
 */
export function panelState(
  failure: PollFailure | null,
  what: string,
): PanelState {
  const reason = failure?.reason;
  if (failure?.status === 403 || reason === "forbidden") {
    return {
      kind: "forbidden",
      message: `You do not have permission to read ${what} on this cluster.`,
      action: null,
    };
  }
  if (reason && RELOAD_REASONS.has(reason)) {
    return {
      kind: "reload",
      message:
        "This cluster's registration changed since the page loaded. Reload to continue.",
      action: "reload",
    };
  }
  if (reason === "conflict") {
    return {
      kind: "conflict",
      message:
        "The configuration changed on the cluster since you loaded it. Your edit is kept; review it against the latest values and save again.",
      action: null,
    };
  }
  if (reason === "outcome_unknown") {
    return {
      kind: "outcome_unknown",
      message:
        "The cluster stopped answering during the save. Reload to check whether the change was applied before trying again.",
      action: "reload",
    };
  }
  if (failure?.status === 429 || reason === "tunnel_limit") {
    return {
      kind: "busy",
      message: `Too many requests to this cluster right now. Try loading ${what} again shortly.`,
      action: "retry",
    };
  }
  return {
    kind: "unavailable",
    message: `Could not read ${what} from this cluster.`,
    action: "retry",
  };
}
