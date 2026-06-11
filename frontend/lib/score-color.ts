// Shared wire types for the backend-computed cluster health object.
// These mirror the JSON shape produced by GET /api/v1/cluster/dashboard-summary.
export type HealthStatus = "healthy" | "degraded" | "critical" | "unknown";
export type SignalStatus = "ok" | "skipped" | "unknown";

export interface HealthSignal {
  name: string;
  status: SignalStatus;
  score: number | null;
  reason?: string;
}

export interface ClusterHealth {
  status: HealthStatus;
  score: number | null;
  signals: HealthSignal[];
  reasons: string[];
}

// scoreColor returns a CSS custom-property color string for a 0–100 numeric
// compliance/health score.  The thresholds and the "alerts" accent case are
// intentionally identical to the original in health-score.ts (R12).
export function scoreColor(
  score: number,
  category?: string,
): string {
  if (score >= 90) {
    return category === "alerts" ? "var(--accent)" : "var(--success)";
  }
  if (score >= 70) return "var(--warning)";
  return "var(--error)";
}

// healthStatusColor maps a categorical HealthStatus to its theme color variable.
// Named healthStatusColor (not statusColor) to avoid colliding with the
// statusColor export in frontend/lib/status-colors.ts, which has a different
// signature and 7+ importers.
export function healthStatusColor(status: HealthStatus | string): string {
  switch (status) {
    case "healthy":
      return "var(--success)";
    case "degraded":
      return "var(--warning)";
    case "critical":
      return "var(--error)";
    default:
      // "unknown" and any unexpected value → muted, never error
      return "var(--text-muted)";
  }
}
