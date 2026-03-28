import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { api } from "@/lib/api.ts";

import { age } from "@/lib/format.ts";
import type { K8sEvent } from "@/lib/k8s-types.ts";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
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
  cpu: {
    percentage: number;
    used: string;
    total: string;
    requests: string;
    limits: string;
  } | null;
  memory: {
    percentage: number;
    used: string;
    total: string;
    requests: string;
    limits: string;
  } | null;
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
      <div>
        <Skeleton class="h-8 w-48 mb-5" />
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(12, 1fr)",
            gap: "16px",
          }}
        >
          <Skeleton
            class="rounded-lg"
            style={{ gridColumn: "span 4", height: "280px" }}
          />
          <div
            style={{
              gridColumn: "span 8",
              display: "grid",
              gridTemplateColumns: "repeat(4, 1fr)",
              gap: "16px",
            }}
          >
            {[1, 2, 3, 4].map((i) => (
              <Skeleton key={i} class="h-32 rounded-lg" />
            ))}
          </div>
          <Skeleton class="h-40 rounded-lg" style={{ gridColumn: "span 6" }} />
          <Skeleton class="h-40 rounded-lg" style={{ gridColumn: "span 6" }} />
          <Skeleton
            class="rounded-lg"
            style={{ gridColumn: "span 7", height: "280px" }}
          />
          <Skeleton
            class="rounded-lg"
            style={{ gridColumn: "span 5", height: "280px" }}
          />
        </div>
      </div>
    );
  }

  const info = clusterInfo.value;
  const s = summary.value;
  const nodeCount = s?.nodes.total ?? info?.nodeCount ?? 0;

  const greeting = (() => {
    if (!IS_BROWSER) return "";
    const hour = new Date().getHours();
    if (hour < 12) return "Good morning";
    if (hour < 17) return "Good afternoon";
    return "Good evening";
  })();

  return (
    <div>
      {/* ===== PAGE HEADER — matches .page-header from mockup ===== */}
      <div class="flex items-center justify-between mb-5">
        <div>
          <h1 class="text-xl font-semibold tracking-tight text-text-primary">
            {greeting ? `${greeting} — ` : ""}Cluster Overview
          </h1>
          {info && (
            <div class="text-xs text-text-muted mt-0.5">
              {info.platform} &middot; Kubernetes {info.kubernetesVersion}
              {` \u00B7 ${nodeCount} node${nodeCount !== 1 ? "s" : ""}`}
            </div>
          )}
        </div>
        <div class="flex gap-2">
          <a
            href="/workloads/deployments/new"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "7px 14px",
              borderRadius: "var(--radius-sm)",
              background: "var(--accent)",
              color: "var(--bg-base)",
              fontSize: "13px",
              fontWeight: 500,
              textDecoration: "none",
              border: "none",
            }}
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              <path d="M4 8h8M8 4v8" />
            </svg>
            Deploy
          </a>
          <a
            href="/tools/yaml-apply"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "7px 14px",
              borderRadius: "var(--radius-sm)",
              background: "transparent",
              color: "var(--text-secondary)",
              fontSize: "13px",
              fontWeight: 500,
              textDecoration: "none",
              border: "1px solid var(--border-primary)",
            }}
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              <rect x="2" y="3" width="12" height="10" rx="1.5" />
              <path d="M5 8h6" />
            </svg>
            YAML
          </a>
        </div>
      </div>

      {/* ===== SINGLE DASHBOARD GRID — matches .dashboard-grid from mockup ===== */}
      <div
        class="stagger-in"
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(12, 1fr)",
          gap: "16px",
        }}
      >
        {/* ===== HEALTH SCORE CARD (span 4) — matches .health-card ===== */}
        <div
          style={{
            gridColumn: "span 4",
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "var(--radius)",
            padding: "20px",
            overflow: "hidden",
          }}
        >
          {/* Card title — matches .card-title */}
          <div
            style={{
              fontSize: "11px",
              fontWeight: 600,
              textTransform: "uppercase",
              letterSpacing: "0.08em",
              color: "var(--text-muted)",
              marginBottom: "16px",
              display: "flex",
              alignItems: "center",
              gap: "6px",
            }}
          >
            <span
              style={{
                width: "6px",
                height: "6px",
                borderRadius: "50%",
                background: "var(--success)",
                display: "inline-block",
              }}
              class="animate-pulse-glow"
            />
            Cluster Health
          </div>
          <HealthScoreRing
            nodes={s?.nodes ?? { total: 0, ready: 0 }}
            pods={s?.pods ?? { total: 0, running: 0, pending: 0, failed: 0 }}
            alerts={s?.alerts ?? { active: 0, critical: 0 }}
          />
        </div>

        {/* ===== METRIC CARDS (span 8) — matches .metrics-grid ===== */}
        <div
          class="stagger-in"
          style={{
            gridColumn: "span 8",
            display: "grid",
            gridTemplateColumns: "repeat(4, 1fr)",
            gridTemplateRows: "1fr",
            gap: "16px",
            alignItems: "stretch",
          }}
        >
          <MetricCard
            value={s?.nodes.total ?? 0}
            label="Nodes"
            status={s?.nodes.ready === s?.nodes.total ? "success" : "warning"}
            statusText={s ? `${s.nodes.ready}/${s.nodes.total} Ready` : "—"}
            href="/cluster/nodes"
            sparklineData={[3, 3, 3, 3, 3, 3, 3, 3]}
            sparklineColor="var(--success)"
            icon={
              <>
                <rect x="2" y="2" width="12" height="12" rx="2" />
                <circle cx="8" cy="8" r="2" />
              </>
            }
          />
          <MetricCard
            value={s?.pods.total ?? 0}
            label="Pods"
            status={(s?.pods.pending ?? 0) > 0 ? "warning" : "success"}
            statusText={s?.pods.total ? `${s.pods.running} Running` : "—"}
            href="/workloads/pods"
            sparklineData={[30, 32, 31, 33, 35, 34, 36, 38, 40, 42, 44, 45]}
            sparklineColor="var(--success)"
            icon={
              <>
                <circle cx="8" cy="8" r="6" />
                <circle cx="8" cy="8" r="2" />
              </>
            }
          />
          <MetricCard
            value={s?.services.total ?? 0}
            label="Services"
            status="success"
            statusText="Active"
            href="/networking/services"
            sparklineData={[40, 41, 41, 42, 42, 43, 43, 44]}
            sparklineColor="var(--success)"
            icon={
              <>
                <path d="M3 8h10M8 3v10" />
                <circle cx="8" cy="8" r="6" />
              </>
            }
          />
          <MetricCard
            value={s?.alerts.active ?? 0}
            label="Alerts"
            status={(s?.alerts.critical ?? 0) > 0
              ? "error"
              : (s?.alerts.active ?? 0) > 0
              ? "warning"
              : "success"}
            statusText={(s?.alerts.active ?? 0) > 0
              ? `${s?.alerts.critical ?? 0} Critical`
              : "\u2713 All Clear"}
            href="/alerting"
            sparklineData={[0, 0, 1, 0, 0, 0, 0, 0]}
            sparklineColor="var(--warning)"
            icon={
              <>
                <path d="M8 2L2 13h12L8 2z" />
                <path d="M8 7v3M8 11.5v.5" />
              </>
            }
          />
        </div>

        {/* ===== CPU UTILIZATION (span 6) — matches .util-card ===== */}
        <div style={{ gridColumn: "span 6" }}>
          <UtilizationGauge
            title="CPU Utilization"
            value={Math.round(s?.cpu?.percentage ?? 0)}
            used={s?.cpu?.used ?? "N/A"}
            total={s?.cpu?.total ?? "N/A"}
            requests={s?.cpu?.requests ?? "—"}
            limits={s?.cpu?.limits ?? "—"}
            color="var(--accent)"
            secondaryColor="var(--success)"
          />
        </div>

        {/* ===== MEMORY UTILIZATION (span 6) — matches .util-card ===== */}
        <div style={{ gridColumn: "span 6" }}>
          <UtilizationGauge
            title="Memory Utilization"
            value={Math.round(s?.memory?.percentage ?? 0)}
            used={s?.memory?.used ?? "N/A"}
            total={s?.memory?.total ?? "N/A"}
            requests={s?.memory?.requests ?? "—"}
            limits={s?.memory?.limits ?? "—"}
            color="var(--accent-secondary)"
            secondaryColor="#FF79C6"
          />
        </div>

        {/* ===== TOPOLOGY (span 7) — matches .topology-card ===== */}
        <div
          style={{
            gridColumn: "span 7",
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "var(--radius)",
            padding: "20px",
            minHeight: "450px",
            overflow: "hidden",
          }}
        >
          <div
            style={{
              fontSize: "11px",
              fontWeight: 600,
              textTransform: "uppercase",
              letterSpacing: "0.08em",
              color: "var(--text-muted)",
              marginBottom: "16px",
              display: "flex",
              alignItems: "center",
              gap: "6px",
            }}
          >
            <span
              style={{
                width: "6px",
                height: "6px",
                borderRadius: "50%",
                background: "var(--success)",
                display: "inline-block",
              }}
              class="animate-pulse-glow"
            />
            Cluster Topology
          </div>
          <ClusterTopology />
        </div>

        {/* ===== EVENTS (span 5) — matches .events-card ===== */}
        <div
          style={{
            gridColumn: "span 5",
            background: "var(--bg-surface)",
            border: "1px solid var(--border-primary)",
            borderRadius: "var(--radius)",
            padding: "20px",
            minHeight: "450px",
            display: "flex",
            flexDirection: "column",
            overflow: "hidden",
          }}
        >
          <div
            style={{
              fontSize: "11px",
              fontWeight: 600,
              textTransform: "uppercase",
              letterSpacing: "0.08em",
              color: "var(--text-muted)",
              marginBottom: "16px",
            }}
          >
            Recent Events
          </div>
          {/* Event list — matches .event-list */}
          <div
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              gap: "2px",
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
                    display: "flex",
                    flexDirection: "column",
                    alignItems: "center",
                    gap: "8px",
                  }}
                >
                  <svg
                    width="24"
                    height="24"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.5"
                    style={{ opacity: 0.4 }}
                  >
                    <circle cx="12" cy="12" r="10" />
                    <path d="M8 14s1.5 2 4 2 4-2 4-2" />
                    <line x1="9" y1="9" x2="9.01" y2="9" />
                    <line x1="15" y1="9" x2="15.01" y2="9" />
                  </svg>
                  <span>All quiet — no recent events</span>
                </div>
              )
              : events.value.map((evt, idx) => {
                const isWarning = evt.type === "Warning";
                const kind = evt.involvedObject?.kind?.toLowerCase() ?? "";
                // Short kind prefix for display: deploy, pod, svc, etc.
                const kindAbbr: Record<string, string> = {
                  deployment: "deploy",
                  service: "svc",
                  replicaset: "rs",
                  statefulset: "sts",
                  daemonset: "ds",
                  persistentvolumeclaim: "pvc",
                  horizontalpodautoscaler: "hpa",
                  configmap: "cm",
                  serviceaccount: "sa",
                  networkpolicy: "netpol",
                };
                const prefix = kindAbbr[kind] ?? kind;
                const resourceLabel = evt.involvedObject?.name
                  ? `${prefix}/${evt.involvedObject.name}`
                  : "";

                return (
                  <div
                    key={`${evt.metadata?.uid ?? idx}`}
                    class="event-row"
                    style={{
                      display: "flex",
                      alignItems: "flex-start",
                      gap: "10px",
                      padding: "8px 10px",
                      borderRadius: "var(--radius-sm)",
                      cursor: "pointer",
                    }}
                  >
                    {/* Event dot — matches .event-dot */}
                    <span
                      style={{
                        width: "7px",
                        height: "7px",
                        borderRadius: "50%",
                        marginTop: "5px",
                        flexShrink: 0,
                        background: isWarning
                          ? "var(--warning)"
                          : "var(--accent)",
                      }}
                    />
                    {/* Event content — matches .event-content */}
                    <div style={{ flex: 1, minWidth: 0 }}>
                      {/* Message line — matches .event-msg */}
                      <div
                        style={{
                          fontSize: "12px",
                          color: "var(--text-secondary)",
                          lineHeight: 1.4,
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {resourceLabel && (
                          <span
                            style={{
                              color: "var(--accent)",
                              fontFamily: "var(--font-mono, monospace)",
                              fontSize: "11px",
                            }}
                          >
                            {resourceLabel}
                          </span>
                        )}
                        {resourceLabel ? " " : ""}
                        {evt.message}
                      </div>
                      {/* Meta line — matches .event-meta */}
                      <div
                        style={{
                          fontSize: "10px",
                          color: "var(--text-muted)",
                          marginTop: "2px",
                          fontFamily: "var(--font-mono, monospace)",
                        }}
                      >
                        {[evt.source?.component, evt.involvedObject?.namespace]
                          .filter(Boolean)
                          .join(" \u00B7 ")}
                      </div>
                    </div>
                    {/* Time — matches .event-time */}
                    <span
                      style={{
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        fontFamily: "var(--font-mono, monospace)",
                        whiteSpace: "nowrap",
                        marginTop: "1px",
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
