export interface HealthMetrics {
  nodesTotal: number;
  nodesReady: number;
  podsTotal: number;
  podsRunning: number;
  podsPending: number;
  podsFailed: number;
  servicesTotal: number;
  activeAlerts: number;
  criticalAlerts: number;
}

export interface HealthScore {
  overall: number;
  nodes: number;
  pods: number;
  services: number;
  alerts: number;
}

const WEIGHTS = {
  nodes: 0.30,
  pods: 0.30,
  services: 0.15,
  alerts: 0.25,
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

  // Services: 100 if any exist
  const services = metrics.servicesTotal > 0 ? 100 : 0;

  // Alerts: deduct for active/critical
  const alerts = clamp(
    100 - (metrics.activeAlerts * 3 + metrics.criticalAlerts * 10),
    0,
    100,
  );

  const overall = Math.round(
    nodes * WEIGHTS.nodes +
      pods * WEIGHTS.pods +
      services * WEIGHTS.services +
      alerts * WEIGHTS.alerts,
  );

  return {
    overall: clamp(overall, 0, 100),
    nodes: Math.round(nodes),
    pods: Math.round(pods),
    services: Math.round(services),
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
