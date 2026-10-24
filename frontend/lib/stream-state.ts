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
}

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
  switch (frame.reason) {
    case "unsupported_platform":
      return `${what} is not available for this cluster yet.`;
    case "forbidden":
      return `You do not have permission for ${what.toLowerCase()} on this cluster.`;
  }
  return `${what} stopped unexpectedly.`;
}
