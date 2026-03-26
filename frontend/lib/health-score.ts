export interface HealthMetrics {
  nodesTotal: number;
  nodesReady: number;
  podsTotal: number;
  podsRunning: number;
  podsPending: number;
  podsFailed: number;
  activeAlerts: number;
  criticalAlerts: number;
}

export interface HealthScore {
  overall: number;
  nodes: number;
  pods: number;
  alerts: number;
}

const WEIGHTS = {
  nodes: 0.35,
  pods: 0.35,
  alerts: 0.30,
};

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

export function calculateHealthScore(metrics: HealthMetrics): HealthScore {
  // Nodes score: ratio of ready to total
  const nodes = metrics.nodesTotal > 0
    ? (metrics.nodesReady / metrics.nodesTotal) * 100
    : 0;

  // Pods score: running ratio minus penalties
  let pods = 0;
  if (metrics.podsTotal > 0) {
    const runningRatio = (metrics.podsRunning / metrics.podsTotal) * 100;
    const penalty = (metrics.podsFailed * 3) + (metrics.podsPending * 1);
    pods = clamp(runningRatio - penalty, 0, 100);
  }

  // Alerts: deduct for active/critical
  const alerts = clamp(
    100 - (metrics.activeAlerts * 3 + metrics.criticalAlerts * 10),
    0,
    100,
  );

  const overall = Math.round(
    nodes * WEIGHTS.nodes +
      pods * WEIGHTS.pods +
      alerts * WEIGHTS.alerts,
  );

  return {
    overall: clamp(overall, 0, 100),
    nodes: Math.round(nodes),
    pods: Math.round(pods),
    alerts: Math.round(alerts),
  };
}

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
