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
  | "busy"
  | "not_installed"
  | "unsupported"
  | "ended";

/** The control an island offers to recover from a state, if any. */
export type RecoveryAction = "retry" | "reload" | "reconnect" | null;

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

/** Whether the failed request read `what` or saved a change to it. */
export type PanelOperation = "read" | "save";

/**
 * The state for a failure reading or saving `what` (e.g. "BGP status").
 * A refusal offers no retry: retrying cannot change the answer (#608 R5).
 */
export function panelState(
  failure: PollFailure | null,
  what: string,
  op: PanelOperation = "read",
): PanelState {
  const reason = failure?.reason;
  if (failure?.status === 403 || reason === "forbidden") {
    return {
      kind: "forbidden",
      message: `You do not have permission to ${op === "save" ? "change" : "read"} ${what} on this cluster.`,
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
      message: `Too many requests to this cluster right now. Try ${op === "save" ? "saving" : "loading"} ${what} again shortly.`,
      action: "retry",
    };
  }
  return {
    kind: "unavailable",
    message:
      op === "save"
        ? `Could not save ${what} on this cluster. Your edit is kept; try again.`
        : `Could not read ${what} from this cluster.`,
    action: "retry",
  };
}

/** The final frame of a live stream, as the viewer received it. */
export interface StreamEnd {
  reason?: string;
}

/**
 * The state for a live flow stream that ended or could not open, once its
 * automatic reconnects are spent or do not apply (#608 KTD11, KTD14). Like
 * panelState it keys on the frame's reason, never its message text.
 */
export function streamState(end: StreamEnd): PanelState {
  switch (end.reason) {
    case "forbidden":
      return {
        kind: "forbidden",
        message: "You do not have permission to view flows on this cluster.",
        action: null,
      };
    case "access_revoked":
      return {
        kind: "forbidden",
        message:
          "Your access to these flows was revoked, so the live stream ended.",
        action: null,
      };
    case "cluster_removed":
      return {
        kind: "reload",
        message:
          "This cluster was removed from k8sCenter, so the live stream ended. Reload to continue.",
        action: "reload",
      };
    case "relay_not_found":
      return {
        kind: "not_installed",
        message:
          "Hubble Relay was not found in kube-system or cilium on this cluster.",
        action: null,
      };
    case "relay_tls_unsupported":
      return {
        kind: "unsupported",
        message:
          "Hubble Relay on this cluster uses TLS, which k8sCenter cannot connect to yet.",
        action: null,
      };
    case "hubble_unavailable":
      return {
        kind: "unsupported",
        message: "Hubble is not available on this cluster.",
        action: null,
      };
    case "unsupported_platform":
      return {
        kind: "unsupported",
        message: "Live flows are not available for this cluster.",
        action: null,
      };
  }
  if (
    end.reason === "cluster_required" ||
    RELOAD_REASONS.has(end.reason ?? "")
  ) {
    return panelState({ status: 409, reason: "cluster_unknown" }, "flows");
  }
  return {
    kind: "ended",
    message: "The live flow stream ended.",
    action: "reconnect",
  };
}
