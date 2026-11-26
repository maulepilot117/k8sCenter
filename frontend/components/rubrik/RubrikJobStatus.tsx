/** In-place status of one Rubrik write (U14, R19).
 *
 * Shows an operation the write endpoints returned and, while it is still
 * open, polls GET /v1/rubrik/operations/{id}, which refreshes a running or
 * unknown operation from RSC before answering. Polling stops when the
 * operation settles, or when the read answers 404: the operation is then no
 * longer visible to this user (for example the read grant was revoked), and
 * the page says so rather than guess its outcome.
 */

import { useEffect, useRef, useState } from "react";
import StatusBadge, { type Tone } from "@/components/ui/glass/StatusBadge.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import type {
  RubrikOperation,
  RubrikOperationKind,
  RubrikOperationState,
} from "@/lib/rubrik-types.ts";

const KIND: Record<RubrikOperationKind, string> = {
  snapshot: "Snapshot",
  sla_assign: "SLA change",
  restore: "Restore",
};

const STATE: Record<RubrikOperationState, { label: string; tone: Tone }> = {
  applying: { label: "Sending", tone: "info" },
  running: { label: "Running", tone: "info" },
  succeeded: { label: "Succeeded", tone: "ok" },
  failed: { label: "Failed", tone: "crit" },
  // Partial is its own outcome: some of the work did not complete.
  partial: { label: "Partial", tone: "warn" },
  unknown: { label: "Outcome unknown", tone: "warn" },
};

/** States the server may still move on; the component keeps polling them. */
const OPEN = new Set<RubrikOperationState>(["applying", "running", "unknown"]);

export function RubrikJobStatus({
  operation,
  pollMs = 5000,
  onSettled,
}: {
  operation: RubrikOperation;
  /** How long to wait between reads while the operation is open. */
  pollMs?: number;
  /** Called once when the operation reaches a final state. */
  onSettled?: (op: RubrikOperation) => void;
}) {
  const [op, setOp] = useState(operation);
  const [gone, setGone] = useState(false);
  const [readFailed, setReadFailed] = useState(false);
  const settled = useRef(false);
  const onSettledRef = useRef(onSettled);
  onSettledRef.current = onSettled;

  useEffect(() => {
    if (gone) return;
    if (!OPEN.has(op.state)) {
      if (!settled.current) {
        settled.current = true;
        onSettledRef.current?.(op);
      }
      return;
    }
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const res = await apiGet<RubrikOperation>(
          `/v1/rubrik/operations/${encodeURIComponent(op.id)}`,
        );
        if (cancelled) return;
        setReadFailed(false);
        setOp(res.data);
      } catch (err) {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 404) {
          setGone(true);
          return;
        }
        // Keep the last known state and try again on the next tick.
        setReadFailed(true);
        setOp((prev) => ({ ...prev }));
      }
    }, pollMs);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [op, gone, pollMs]);

  const state = STATE[op.state] ?? STATE.unknown;
  return (
    <div
      role="status"
      data-testid={`rubrik-op-${op.id}`}
      className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-elevated px-3 py-2 text-[13px]"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-semibold text-text-primary">
          {KIND[op.kind] ?? "Operation"} · {op.namespace}
        </span>
        {gone ? (
          <StatusBadge label="No longer visible" tone="neutral" />
        ) : (
          <StatusBadge label={state.label} tone={state.tone} />
        )}
      </div>
      {gone && (
        <p className="m-0 text-text-muted">
          This operation is no longer visible to you, so its outcome is not
          shown.
        </p>
      )}
      {!gone && op.message && (
        <p className="m-0 text-text-secondary">{op.message}</p>
      )}
      {!gone && op.stillProtected === true && (
        <p className="m-0 text-text-muted">
          Another object you can see still protects the namespace.
        </p>
      )}
      {!gone && op.stillProtected === false && (
        <p className="m-0 text-text-muted">
          No other object you can see protects the namespace.
        </p>
      )}
      {!gone && op.replayed && (
        <p className="m-0 text-text-muted">
          This repeated an earlier request, so nothing was sent again.
        </p>
      )}
      {!gone && (op.refreshError || readFailed) && OPEN.has(op.state) && (
        <p className="m-0 text-text-muted">
          The latest state could not be read; showing the last known one.
        </p>
      )}
    </div>
  );
}
