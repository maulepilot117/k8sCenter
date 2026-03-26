import { useMemo } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { GaugeRing } from "@/components/ui/GaugeRing.tsx";
import { calculateHealthScore, scoreColor } from "@/lib/health-score.ts";
import type { HealthMetrics, HealthScore } from "@/lib/health-score.ts";

interface HealthScoreRingProps {
  nodes: { total: number; ready: number };
  pods: { total: number; running: number; pending: number; failed: number };
  alerts: { active: number; critical: number };
}

export default function HealthScoreRing(
  { nodes, pods, alerts }: HealthScoreRingProps,
) {
  const score: HealthScore = useMemo(() => {
    const metrics: HealthMetrics = {
      nodesTotal: nodes.total,
      nodesReady: nodes.ready,
      podsTotal: pods.total,
      podsRunning: pods.running,
      podsPending: pods.pending,
      podsFailed: pods.failed,
      activeAlerts: alerts.active,
      criticalAlerts: alerts.critical,
    };
    return calculateHealthScore(metrics);
  }, [nodes, pods, alerts]);

  if (!IS_BROWSER) {
    return (
      <div
        style={{
          width: "100%",
          padding: "20px",
          background: "var(--bg-surface)",
          borderRadius: "12px",
        }}
      />
    );
  }

  const subScores = [
    { label: "Nodes", value: score.nodes, category: "nodes" },
    { label: "Pods", value: score.pods, category: "pods" },
    { label: "Alerts", value: score.alerts, category: "alerts" },
  ];

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: "16px",
        padding: "20px",
        background: "var(--bg-surface)",
        borderRadius: "12px",
        border: "1px solid var(--border-subtle)",
      }}
    >
      {/* Main gauge */}
      <GaugeRing
        value={score.overall}
        size={160}
        strokeWidth={8}
        color="var(--accent)"
        secondaryColor="var(--success)"
        label="Health"
        displayValue={String(score.overall)}
        valueSize="42px"
        valueGradient
      />

      {/* Sub-scores */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(3, 1fr)",
          gap: "8px",
          width: "100%",
        }}
      >
        {subScores.map((sub) => (
          <div
            key={sub.label}
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              padding: "8px 12px",
              borderRadius: "8px",
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-subtle)",
            }}
          >
            <span
              style={{
                fontSize: "18px",
                fontWeight: 600,
                fontFamily: "var(--font-mono, monospace)",
                color: scoreColor(sub.value, sub.category),
              }}
            >
              {sub.value}
            </span>
            <span
              style={{
                fontSize: "10px",
                color: "var(--text-muted)",
                marginTop: "2px",
              }}
            >
              {sub.label}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
