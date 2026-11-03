import { type ClusterTarget, UNKNOWN_GENERATION } from "@/src/lib/cluster.ts";

/**
 * The first frame every live stream sends. A WebSocket carries no
 * X-Cluster-ID header, and the server refuses a stream that names no cluster
 * rather than assuming the local one (#608 KTD10), so the cluster travels
 * in-band. Pass the target pinned when the session opened, not a fresh read:
 * the stream must stay on the cluster it was opened for. The `unknown`
 * sentinel names no registration, so it is not sent as a generation.
 */
export function streamAuthFrame(token: string, target: ClusterTarget) {
  return {
    type: "auth",
    token,
    clusterId: target.clusterId,
    ...(target.generation === UNKNOWN_GENERATION
      ? {}
      : { generation: target.generation }),
  };
}

export interface StreamErrorFrame {
  reason?: string;
  message?: string;
  /** The server marks an end the client may resume from by reconnecting. */
  retryable?: boolean;
}

/**
 * Backoff before each automatic reconnect after a retryable end: three
 * tries, reset once a stream subscribes again. The flow viewer still keeps
 * its own copy of this schedule.
 */
export const RECONNECT_DELAYS_MS = [2_000, 5_000, 10_000];

// The cluster-identity refusals the WebSocket auth helper sends
// (backend/internal/server/ws_helpers.go). They differ from the HTTP panel
// reasons networking-state.ts reloads on, because the two paths refuse
// differently.
const RELOAD_REASONS = new Set([
  "cluster_required",
  "cluster_invalid",
  "cluster_pin_mismatch",
  "cluster_generation_mismatch",
]);

/**
 * The copy for a live stream's error frame. A frame with a reason keys on
 * that reason, never its message text, so server wording never reaches the
 * page for a refusal. A frame without a reason carries one of the handler's
 * own fixed sentences (an invalid container name, a missing permission), which
 * is shown as sent.
 */
export function streamErrorMessage(
  frame: StreamErrorFrame,
  what: string,
): string {
  if (!frame.reason) return frame.message ?? `${what} stopped.`;
  if (RELOAD_REASONS.has(frame.reason)) {
    return "The selected cluster changed or could not be confirmed. Reload the page to continue.";
  }
  const subject = what.toLowerCase();
  switch (frame.reason) {
    case "unsupported_platform":
      return `${what} is not available for this cluster yet.`;
    case "forbidden":
      return `You do not have permission for ${subject} on this cluster.`;
    // The shared stream lifecycle's ends (stream_lifecycle.go). The two
    // retryable ones reach the page only once the reconnects are spent.
    case "token_expired":
      return `${what} stopped when your session expired and could not reconnect.`;
    case "server_restarting":
      return `${what} stopped while k8sCenter restarted and could not reconnect.`;
    case "cluster_removed":
      return `The cluster was removed from k8sCenter, so ${subject} ended.`;
    case "access_revoked":
      return `Your access was revoked, so ${subject} ended.`;
    case "signed_out":
      return `You signed out, so ${subject} ended.`;
    case "idle_timeout":
      return `${what} was idle too long and ended.`;
    // The client's own reason for a resume whose socket closed before it
    // subscribed; never sent by the server.
    case "connect_failed":
      return `${what} could not reconnect.`;
    // A remote stream that could not reach its cluster
    // (k8s.ClassifyTargetErr).
    case "unreachable":
      return "The selected cluster could not be reached.";
    case "cluster_unknown":
      return "The selected cluster is no longer registered.";
    case "credentials_invalid":
      return "k8sCenter could not connect to the selected cluster with its stored credentials.";
    case "db_unavailable":
      return "The cluster registry is unavailable. Try again shortly.";
  }
  return `${what} stopped unexpectedly.`;
}

// --- Resuming a log tail ---

/**
 * The timestamp a log line starts with, as `[seconds, nanoseconds]`, or
 * null when it has none. The tail asks for timestamps, which Kubernetes
 * writes in RFC 3339 with a fraction whose trailing zeros are trimmed, so
 * the fraction is padded before it is compared.
 */
export function logLineTimestamp(line: string): [string, number] | null {
  const m = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?Z\s/.exec(line);
  if (!m) return null;
  return [m[1], Number((m[2] ?? "").padEnd(9, "0"))];
}

/** Whether timestamp a is later than b. */
export function laterThan(a: [string, number], b: [string, number]): boolean {
  return a[0] === b[0] ? a[1] > b[1] : a[0] > b[0];
}

/**
 * Whether a line a resumed tail sends repeats what is already shown. A
 * resume asks for the last lines again, so a line at or before the last one
 * shown (`after`) is a replay. A line with no timestamp cannot be placed and
 * is kept.
 */
export function isReplayedLine(
  line: string,
  after: [string, number] | null,
): boolean {
  if (!after) return false;
  const ts = logLineTimestamp(line);
  return ts !== null && !laterThan(ts, after);
}
