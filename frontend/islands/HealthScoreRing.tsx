import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { GaugeRing } from "@/components/ui/GaugeRing.tsx";
import { calculateHealthScore, scoreColor } from "@/lib/health-score.ts";
import type { HealthMetrics, HealthScore } from "@/lib/health-score.ts";

interface NodeItem {
  status?: {
    conditions?: { type: string; status: string }[];
  };
}

interface PodItem {
  status?: {
    phase?: string;
  };
}

interface AlertItem {
  labels?: {
    severity?: string;
  };
}

const REFRESH_INTERVAL = 60_000;

export default function HealthScoreRing() {
  const score = useSignal<HealthScore | null>(null);
  const loading = useSignal(true);
  const error = useSignal(false);

  async function fetchMetrics(): Promise<HealthMetrics> {
    const [nodesRes, podsRes, servicesRes, alertsRes] = await Promise
      .allSettled([
        apiGet<NodeItem[]>("/v1/resources/nodes"),
        apiGet<PodItem[]>("/v1/resources/pods"),
        apiGet<unknown>("/v1/resources/services?limit=1"),
        apiGet<AlertItem[]>("/v1/alerts"),
      ]);

    // Nodes
    let nodesTotal = 0;
    let nodesReady = 0;
    if (nodesRes.status === "fulfilled" && Array.isArray(nodesRes.value.data)) {
      const nodes = nodesRes.value.data;
      nodesTotal = nodes.length;
      nodesReady = nodes.filter((n) =>
        n.status?.conditions?.some((c) =>
          c.type === "Ready" && c.status === "True"
        )
      ).length;
    }

    // Pods
    let podsTotal = 0;
    let podsRunning = 0;
    let podsPending = 0;
    let podsFailed = 0;
    if (podsRes.status === "fulfilled" && Array.isArray(podsRes.value.data)) {
      const pods = podsRes.value.data;
      podsTotal = pods.length;
      for (const p of pods) {
        const phase = p.status?.phase?.toLowerCase();
        if (phase === "running" || phase === "succeeded") podsRunning++;
        else if (phase === "pending") podsPending++;
        else if (phase === "failed") podsFailed++;
      }
    }

    // Services
    let servicesTotal = 0;
    if (servicesRes.status === "fulfilled") {
      servicesTotal = servicesRes.value.metadata?.total ?? 0;
    }

    // Alerts
    let activeAlerts = 0;
    let criticalAlerts = 0;
    if (
      alertsRes.status === "fulfilled" && Array.isArray(alertsRes.value.data)
    ) {
      const alerts = alertsRes.value.data;
      activeAlerts = alerts.length;
      criticalAlerts = alerts.filter((a) =>
        a.labels?.severity === "critical"
      ).length;
    }

    return {
      nodesTotal,
      nodesReady,
      podsTotal,
      podsRunning,
      podsPending,
      podsFailed,
      servicesTotal,
      activeAlerts,
      criticalAlerts,
    };
  }

  async function loadScore() {
    if (document.hidden) return;
    try {
      const metrics = await fetchMetrics();
      score.value = calculateHealthScore(metrics);
      error.value = false;
    } catch {
      error.value = true;
      // Keep last known score if we had one
      if (!score.value) {
        score.value = {
          overall: 0,
          nodes: 0,
          pods: 0,
          services: 0,
          alerts: 0,
        };
      }
    } finally {
      loading.value = false;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    loadScore();
    const interval = setInterval(loadScore, REFRESH_INTERVAL);
    return () => clearInterval(interval);
  }, []);

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

  if (loading.value) {
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
        {/* Skeleton ring */}
        <div
          style={{
            width: "160px",
            height: "160px",
            borderRadius: "50%",
            background: "var(--bg-elevated)",
            animation: "pulse 2s ease-in-out infinite",
          }}
        />
        {/* Skeleton sub-scores */}
        <div style={{ display: "flex", gap: "8px" }}>
          {[1, 2, 3, 4].map((i) => (
            <div
              key={i}
              style={{
                width: "64px",
                height: "48px",
                borderRadius: "8px",
                background: "var(--bg-elevated)",
                animation: "pulse 2s ease-in-out infinite",
              }}
            />
          ))}
        </div>
      </div>
    );
  }

  const s = score.value!;
  const subScores = [
    { label: "Nodes", value: s.nodes, category: "nodes" },
    { label: "Pods", value: s.pods, category: "pods" },
    { label: "Services", value: s.services, category: "services" },
    { label: "Alerts", value: s.alerts, category: "alerts" },
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
        value={s.overall}
        size={160}
        strokeWidth={10}
        color="var(--accent)"
        secondaryColor="var(--success)"
        label="Health"
      />

      {error.value && (
        <div
          style={{
            fontSize: "11px",
            color: "var(--warning)",
            textAlign: "center",
          }}
        >
          Some metrics unavailable
        </div>
      )}

      {/* Sub-scores */}
      <div
        style={{
          display: "flex",
          gap: "8px",
          width: "100%",
          justifyContent: "center",
          flexWrap: "wrap",
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
              minWidth: "64px",
            }}
          >
            <span
              style={{
                fontSize: "18px",
                fontWeight: 700,
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
