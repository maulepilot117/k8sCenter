import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { api } from "@/lib/api.ts";

import { age } from "@/lib/format.ts";
import type { K8sEvent } from "@/lib/k8s-types.ts";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import HealthScoreRing from "@/islands/HealthScoreRing.tsx";
import MetricCard from "@/islands/MetricCard.tsx";
import UtilizationGauge from "@/islands/UtilizationGauge.tsx";
import ClusterTopology from "@/islands/ClusterTopology.tsx";

interface ClusterInfoData {
  clusterID: string;
  kubernetesVersion: string;
  platform: string;
  nodeCount: number;
  kubecenter: {
    version: string;
    commit: string;
    buildDate: string;
  };
}

interface DashboardSummary {
  nodes: { total: number; ready: number };
  pods: { total: number; running: number; pending: number; failed: number };
  services: { total: number };
  alerts: { active: number; critical: number };
  cpu: { percentage: number } | null;
  memory: { percentage: number } | null;
}

const REFRESH_INTERVAL = 60_000;

export default function DashboardV2() {
  const clusterInfo = useSignal<ClusterInfoData | null>(null);
  const summary = useSignal<DashboardSummary | null>(null);
  const events = useSignal<K8sEvent[]>([]);
  const loading = useSignal(true);

  async function fetchSummary(signal?: AbortSignal) {
    const summaryRes = await api<DashboardSummary>(
      "/v1/cluster/dashboard-summary",
      { method: "GET", signal },
    );
    if (summaryRes.data) {
      summary.value = summaryRes.data;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;

    const controller = new AbortController();

    async function load() {
      loading.value = true;

      const [infoRes, _summaryResult, eventsRes] = await Promise.allSettled([
        api<ClusterInfoData>("/v1/cluster/info", {
          method: "GET",
          signal: controller.signal,
        }),
        fetchSummary(controller.signal),
        api<K8sEvent[]>("/v1/resources/events?limit=10", {
          method: "GET",
          signal: controller.signal,
        }),
      ]);

      if (controller.signal.aborted) return;

      if (infoRes.status === "fulfilled") {
        clusterInfo.value = infoRes.value.data;
      }

      if (
        eventsRes.status === "fulfilled" &&
        Array.isArray(eventsRes.value.data)
      ) {
        events.value = eventsRes.value.data;
      }

      loading.value = false;
    }

    load();

    // 60-second auto-refresh for summary data
    const interval = setInterval(async () => {
      if (document.hidden) return;
      try {
        await fetchSummary();
      } catch {
        // Keep last known data on error
      }
    }, REFRESH_INTERVAL);

    return () => {
      controller.abort();
      clearInterval(interval);
    };
  }, []);

  if (!IS_BROWSER) {
    return <div style={{ minHeight: "400px" }} />;
  }

  if (loading.value) {
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
        <Skeleton class="h-10 w-64" />
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(12, 1fr)",
            gap: "16px",
          }}
        >
          <div style={{ gridColumn: "span 4" }}>
            <Skeleton class="h-56 w-full rounded-lg" />
          </div>
          <div style={{ gridColumn: "span 8" }}>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1fr 1fr",
                gap: "12px",
              }}
            >
              {[1, 2, 3, 4].map((i) => (
                <Skeleton key={i} class="h-28 w-full rounded-lg" />
              ))}
            </div>
          </div>
        </div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "16px",
          }}
        >
          <Skeleton class="h-36 w-full rounded-lg" />
          <Skeleton class="h-36 w-full rounded-lg" />
        </div>
      </div>
    );
  }

  const info = clusterInfo.value;
  const s = summary.value;
  const nodeCount = s?.nodes.total ?? info?.nodeCount ?? 0;

  const metricCards = [
    {
      value: s?.nodes.total ?? 0,
      label: "Nodes",
      status: (s?.nodes.total ?? 0) > 0 ? "success" : "warning",
      statusText: s ? `${s.nodes.ready}/${s.nodes.total} Ready` : "None",
      href: "/cluster/nodes",
    },
    {
      value: s?.pods.total ?? 0,
      label: "Pods",
      status: (s?.pods.total ?? 0) > 0 ? "success" : "info",
      statusText: s?.pods.total ? `${s.pods.running} Running` : "None",
      href: "/workloads/pods",
    },
    {
      value: s?.services.total ?? 0,
      label: "Services",
      status: "info" as const,
      statusText: "Active",
      href: "/networking/services",
    },
    {
      value: s?.alerts.active ?? 0,
      label: "Alerts",
      status: (s?.alerts.critical ?? 0) > 0
        ? "error"
        : (s?.alerts.active ?? 0) > 0
        ? "warning"
        : "success",
      statusText: (s?.alerts.active ?? 0) > 0
        ? `${s?.alerts.critical ?? 0} Critical`
        : "None",
      href: "/alerts",
    },
  ] as const;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Header row */}
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "flex-start",
        }}
      >
        <div>
          <h1
            style={{
              fontSize: "24px",
              fontWeight: 700,
              color: "var(--text-primary)",
              margin: 0,
            }}
          >
            Cluster Overview
          </h1>
          {info && (
            <p
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                marginTop: "4px",
              }}
            >
              {info.platform} &middot; Kubernetes {info.kubernetesVersion}
              {` \u00B7 ${nodeCount} node${nodeCount !== 1 ? "s" : ""}`}
            </p>
          )}
        </div>
        <div style={{ display: "flex", gap: "8px" }}>
          <a
            href="/workloads/deployments/new"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "8px 14px",
              borderRadius: "var(--radius)",
              background: "var(--accent)",
              color: "#fff",
              fontSize: "13px",
              fontWeight: 500,
              textDecoration: "none",
              border: "none",
            }}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <circle cx="12" cy="12" r="10" />
              <path d="M8 12h8M12 8v8" />
            </svg>
            Deploy
          </a>
          <a
            href="/tools/yaml-apply"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "8px 14px",
              borderRadius: "var(--radius)",
              background: "var(--bg-elevated)",
              color: "var(--text-primary)",
              fontSize: "13px",
              fontWeight: 500,
              textDecoration: "none",
              border: "1px solid var(--border-primary)",
            }}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path d="M16 18l2-2-2-2M8 18l-2-2 2-2M12 10l-2 8" />
            </svg>
            YAML
          </a>
        </div>
      </div>

      {/* Health Score + Metric Cards row */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(12, 1fr)",
          gap: "16px",
        }}
      >
        {/* Health Score */}
        <div
          style={{
            gridColumn: "span 4",
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "var(--radius)",
            padding: "16px",
            display: "flex",
            flexDirection: "column",
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              marginBottom: "12px",
            }}
          >
            <StatusDot status="success" pulse size={8} />
            <span
              style={{
                fontSize: "13px",
                fontWeight: 600,
                color: "var(--text-primary)",
              }}
            >
              Cluster Health
            </span>
          </div>
          <HealthScoreRing
            nodes={s?.nodes ?? { total: 0, ready: 0 }}
            pods={s?.pods ?? {
              total: 0,
              running: 0,
              pending: 0,
              failed: 0,
            }}
            alerts={s?.alerts ?? { active: 0, critical: 0 }}
          />
        </div>

        {/* Metric Cards */}
        <div
          style={{
            gridColumn: "span 8",
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "12px",
          }}
        >
          {metricCards.map((card) => (
            <MetricCard
              key={card.label}
              value={card.value}
              label={card.label}
              status={card.status as "success" | "warning" | "error" | "info"}
              statusText={card.statusText}
              href={card.href}
            />
          ))}
        </div>
      </div>

      {/* CPU + Memory Utilization row */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(12, 1fr)",
          gap: "16px",
        }}
      >
        <div style={{ gridColumn: "span 6" }}>
          <UtilizationGauge
            title="CPU Utilization"
            value={summary.value?.cpu?.percentage ?? 0}
            used={summary.value?.cpu
              ? `${summary.value.cpu.percentage.toFixed(1)}%`
              : "N/A"}
            total="100%"
            color="var(--accent)"
            secondaryColor="var(--accent-secondary)"
          />
        </div>
        <div style={{ gridColumn: "span 6" }}>
          <UtilizationGauge
            title="Memory Utilization"
            value={summary.value?.memory?.percentage ?? 0}
            used={summary.value?.memory
              ? `${summary.value.memory.percentage.toFixed(1)}%`
              : "N/A"}
            total="100%"
            color="var(--accent-secondary)"
            secondaryColor="var(--accent)"
          />
        </div>
      </div>

      {/* Topology + Events row */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(12, 1fr)",
          gap: "16px",
        }}
      >
        {/* Cluster Topology placeholder */}
        <div
          style={{
            gridColumn: "span 7",
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "var(--radius)",
            padding: "16px",
            minHeight: "280px",
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              marginBottom: "16px",
            }}
          >
            <StatusDot status="info" pulse size={8} />
            <span
              style={{
                fontSize: "13px",
                fontWeight: 600,
                color: "var(--text-primary)",
              }}
            >
              Cluster Topology
            </span>
          </div>
          <ClusterTopology />
        </div>

        {/* Recent Events */}
        <div
          style={{
            gridColumn: "span 5",
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "var(--radius)",
            padding: "16px",
            minHeight: "280px",
            display: "flex",
            flexDirection: "column",
          }}
        >
          <div
            style={{
              fontSize: "13px",
              fontWeight: 600,
              color: "var(--text-primary)",
              marginBottom: "12px",
            }}
          >
            Recent Events
          </div>
          <div
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              gap: "6px",
              overflow: "auto",
            }}
          >
            {events.value.length === 0
              ? (
                <div
                  style={{
                    color: "var(--text-muted)",
                    fontSize: "12px",
                    textAlign: "center",
                    paddingTop: "40px",
                  }}
                >
                  No recent events
                </div>
              )
              : events.value.map((evt, idx) => {
                const isWarning = evt.type === "Warning";
                return (
                  <div
                    key={`${evt.metadata?.uid ?? idx}`}
                    style={{
                      display: "flex",
                      alignItems: "flex-start",
                      gap: "8px",
                      padding: "6px 0",
                      borderBottom: idx < events.value.length - 1
                        ? "1px solid var(--border-subtle)"
                        : "none",
                    }}
                  >
                    <div style={{ paddingTop: "4px", flexShrink: 0 }}>
                      <StatusDot
                        status={isWarning ? "warning" : "info"}
                        size={6}
                      />
                    </div>
                    <div
                      style={{
                        flex: 1,
                        minWidth: 0,
                        fontSize: "12px",
                        color: "var(--text-secondary)",
                        lineHeight: "1.4",
                      }}
                    >
                      {evt.involvedObject && (
                        <span
                          style={{
                            color: "var(--accent)",
                            fontFamily: "var(--font-mono, monospace)",
                            fontWeight: 500,
                          }}
                        >
                          {evt.involvedObject.name}
                          {""}
                        </span>
                      )}
                      <span
                        style={{
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                          display: "inline",
                        }}
                      >
                        {evt.message}
                      </span>
                    </div>
                    <span
                      style={{
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        flexShrink: 0,
                        whiteSpace: "nowrap",
                      }}
                    >
                      {evt.metadata?.creationTimestamp
                        ? age(evt.metadata.creationTimestamp)
                        : ""}
                    </span>
                  </div>
                );
              })}
          </div>
        </div>
      </div>
    </div>
  );
}
