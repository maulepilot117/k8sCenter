/**
 * The severity roll-up behind the namespace diagnostics widget.
 *
 * `GET /v1/diagnostics/{namespace}/summary` answers with a flat list of
 * failing pods and a total, and the card shows counts rather than the list:
 * a widget four columns wide has room for a verdict, not an inventory. Turning
 * one into the other is the part an operator acts on, so it is pure and lives
 * here rather than inside the component (D-10).
 *
 * The one judgement in this file is the direction it errs in. A reason the
 * backend reports and this build has never heard of counts as critical, not as
 * a warning: the server has already decided the pod is failing, and rendering
 * a failure mode added later as the milder of the two available severities is
 * the same defect as rendering an absent feature as a healthy one.
 */
import type { DiagnosticsSummary } from "./wire-types.ts";

/** How loudly the card reports a failing pod. Two levels, because the payload
 * supports two distinctions and inventing a third would be decoration. */
export type DiagnosticsSeverity = "critical" | "warning";

/**
 * The reasons that are a delay rather than a failure to start.
 *
 * `podFailureReason` in backend/internal/diagnostics/handler.go emits exactly
 * four strings today: "Pending", and the three back-off reasons. Only the
 * first is a pod that may yet come up on its own, so it is the only one named
 * here and everything else -- known or not -- is critical. See the module
 * docstring for why the default runs this way round.
 */
const WARNING_REASONS: ReadonlySet<string> = new Set(["Pending"]);

export function severityOf(reason: string): DiagnosticsSeverity {
  return WARNING_REASONS.has(reason) ? "warning" : "critical";
}

export interface DiagnosticsReasonCount {
  reason: string;
  count: number;
  severity: DiagnosticsSeverity;
}

export interface DiagnosticsRollUp {
  /** Pods the namespace holds, as the backend counted them. */
  total: number;
  /** Pods the backend reported as failing. */
  failing: number;
  /** The rest. Never negative, whatever the payload claims. */
  healthy: number;
  critical: number;
  warning: number;
  /** Failing reasons, most frequent first and alphabetical within a count. */
  reasons: DiagnosticsReasonCount[];
  /**
   * The namespace holds no workloads at all.
   *
   * Kept apart from "healthy" deliberately, and it is the single most
   * important distinction this module draws. A namespace with ten running
   * pods and nothing wrong is good news. A namespace with nothing in it is
   * not news -- and it is also the shape a DELETED namespace comes back as,
   * because the handler lists pods out of the informer cache and a namespace
   * that no longer exists simply has none. Reporting that as "all healthy"
   * would put a green card over a namespace that is gone, which is exactly
   * the failure the availability states exist to prevent.
   */
  empty: boolean;
}

const EMPTY: DiagnosticsRollUp = {
  total: 0,
  failing: 0,
  healthy: 0,
  critical: 0,
  warning: 0,
  reasons: [],
  empty: true,
};

/**
 * Folds a summary payload into the counts the card renders.
 *
 * Total, because a payload that cannot be read must not render as a healthy
 * namespace: a missing body, a missing total or a `failing` that is not an
 * array all come back as the empty roll-up, which the card reports as "nothing
 * to show" rather than as a clean bill of health.
 */
export function rollUpDiagnostics(
  data: DiagnosticsSummary | null | undefined,
): DiagnosticsRollUp {
  if (data === null || data === undefined) return EMPTY;

  const total = typeof data.total === "number" ? data.total : 0;
  const failures = Array.isArray(data.failing) ? data.failing : [];

  const counts = new Map<string, number>();
  let critical = 0;
  let warning = 0;
  for (const failure of failures) {
    const reason = failure?.reason ?? "";
    counts.set(reason, (counts.get(reason) ?? 0) + 1);
    if (severityOf(reason) === "critical") critical++;
    else warning++;
  }

  const reasons = [...counts.entries()]
    .map(([reason, count]) => ({ reason, count, severity: severityOf(reason) }))
    // Count first so the loudest reason leads, then alphabetical so two
    // refreshes returning the same counts do not reshuffle the card.
    .sort((a, b) => b.count - a.count || a.reason.localeCompare(b.reason));

  return {
    total,
    failing: failures.length,
    healthy: Math.max(0, total - failures.length),
    critical,
    warning,
    reasons,
    empty: total === 0 && failures.length === 0,
  };
}
