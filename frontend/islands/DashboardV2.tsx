import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { api } from "@/lib/api.ts";

import { age, percentile } from "@/lib/format.ts";
import type { K8sEvent } from "@/lib/k8s-types.ts";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import Gauge from "@/components/charts/Gauge.tsx";
import Donut from "@/components/charts/Donut.tsx";
import type { DonutSegment } from "@/components/charts/Donut.tsx";
import BarRow from "@/components/charts/BarRow.tsx";
import { healthStatusColor } from "@/lib/score-color.ts";
import type { ClusterHealth } from "@/lib/score-color.ts";
import { ResourceAreaChart } from "@/components/charts/ResourceAreaChart.tsx";
import { MetricTile } from "@/components/ui/MetricTile.tsx";
import { NetworkTile } from "@/components/ui/NetworkTile.tsx";
import { CheckItem } from "@/components/ui/CheckItem.tsx";

// ─── Wire types (unchanged from original) ────────────────────────────────────

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
  health?: ClusterHealth;
}

// DashboardTrends mirrors the backend payload from GET
// /v1/cluster/dashboard-trends — short historical series (oldest→newest) that
// back the metric-card sparklines. Any series may be empty when Prometheus or
// kube-state-metrics is unavailable; the cards then render no sparkline.
interface DashboardTrends {
  nodes: number[] | null;
  pods: number[] | null;
  services: number[] | null;
  cpu: number[] | null;
  memory: number[] | null;
  // Cluster-wide network throughput in Mbps (oldest→newest). The Network I/O
  // tile derives its displayed RX/TX p95 from these series, so the value tracks
  // whichever time-range tab is active.
  networkRx: number[] | null;
  networkTx: number[] | null;
  window: string;
  step: string;
}

const REFRESH_INTERVAL = 60_000;

// ─── Time range tabs ─────────────────────────────────────────────────────────

const TIME_RANGES = ["15m", "1h", "6h", "24h"] as const;
type TimeRange = typeof TIME_RANGES[number];

// ─── Main dashboard ───────────────────────────────────────────────────────────

export default function DashboardV2() {
  const clusterInfo = useSignal<ClusterInfoData | null>(null);
  const summary = useSignal<DashboardSummary | null>(null);
  const trends = useSignal<DashboardTrends | null>(null);
  const events = useSignal<K8sEvent[]>([]);
  const loading = useSignal(true);
  // timeRange is the selected tab; trendsPeriod is the range the currently
  // displayed series actually belongs to. They differ only while a tab-switch
  // fetch is in flight — the NetworkTile label reads trendsPeriod so it never
  // shows a window the data hasn't caught up to yet.
  const timeRange = useSignal<TimeRange>("1h");
  const trendsPeriod = useSignal<TimeRange>("1h");
  const syncedAgo = useSignal<string>("");
  // Cancels the prior in-flight tab-switch trends fetch so a slower earlier
  // response can't land after a newer one and clobber trends.value out of order.
  const tabAbort = useRef<AbortController | null>(null);

  async function fetchSummary(signal?: AbortSignal) {
    const summaryRes = await api<DashboardSummary>(
      "/v1/cluster/dashboard-summary",
      { method: "GET", signal },
    );
    if (summaryRes.data) {
      summary.value = summaryRes.data;
      syncedAgo.value = "just now";
    }
  }

  async function fetchTrends(range: TimeRange, signal?: AbortSignal) {
    const trendsRes = await api<DashboardTrends>(
      `/v1/cluster/dashboard-trends?range=${range}`,
      { method: "GET", signal },
    );
    if (trendsRes.data) {
      trends.value = trendsRes.data;
      trendsPeriod.value = range;
    }
  }

  // Tab-switch fetch: abort any prior tab fetch first, then issue the new one
  // under a fresh signal. Errors (including AbortError) are swallowed — the 60s
  // interval refresh self-heals, and an aborted request is expected, not a fault.
  function fetchTrendsForTab(range: TimeRange) {
    tabAbort.current?.abort();
    const controller = new AbortController();
    tabAbort.current = controller;
    fetchTrends(range, controller.signal).catch(() => {});
  }

  useEffect(() => {
    if (!IS_BROWSER) return;

    const controller = new AbortController();

    async function load() {
      loading.value = true;

      const [infoRes, _summaryResult, _trendsResult, eventsRes] = await Promise
        .allSettled([
          api<ClusterInfoData>("/v1/cluster/info", {
            method: "GET",
            signal: controller.signal,
          }),
          fetchSummary(controller.signal),
          fetchTrends(timeRange.value, controller.signal),
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

    const interval = setInterval(() => {
      if (document.hidden) return;
      // allSettled never rejects; both fetches share the mount signal so they
      // cancel on unmount rather than writing to a torn-down island.
      Promise.allSettled([
        fetchSummary(controller.signal),
        fetchTrends(timeRange.value, controller.signal),
      ]);
    }, REFRESH_INTERVAL);

    return () => {
      controller.abort();
      tabAbort.current?.abort();
      clearInterval(interval);
    };
  }, []);

  if (!IS_BROWSER) {
    return <div style={{ minHeight: "400px" }} />;
  }

  // ─── Loading skeletons ───────────────────────────────────────────────────

  if (loading.value) {
    return (
      <div>
        <Skeleton class="h-8 w-56 mb-2" />
        <Skeleton class="h-4 w-80 mb-6" />
        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: "var(--grid-gap, 16px)",
          }}
        >
          <Skeleton
            style={{ flex: "2 1 380px", height: "200px", borderRadius: "18px" }}
          />
          <div
            style={{
              flex: "3 1 380px",
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: "var(--grid-gap, 16px)",
            }}
          >
            {[1, 2, 3, 4].map((i) => (
              <Skeleton
                key={i}
                style={{ height: "120px", borderRadius: "18px" }}
              />
            ))}
          </div>
          <Skeleton
            style={{ flex: "3 1 380px", height: "160px", borderRadius: "18px" }}
          />
          <Skeleton
            style={{ flex: "2 1 240px", height: "160px", borderRadius: "18px" }}
          />
          <Skeleton
            style={{ flex: "2 1 240px", height: "200px", borderRadius: "18px" }}
          />
          <Skeleton
            style={{ flex: "2 1 240px", height: "200px", borderRadius: "18px" }}
          />
          <Skeleton
            style={{ flex: "2 1 240px", height: "200px", borderRadius: "18px" }}
          />
        </div>
      </div>
    );
  }

  // ─── Derived values ──────────────────────────────────────────────────────

  const info = clusterInfo.value;
  const s = summary.value;
  const t = trends.value;
  const nodeCount = s?.nodes.total ?? info?.nodeCount ?? 0;
  const podCount = s?.pods.total ?? 0;
  const clusterName = info?.platform ?? info?.clusterID ?? "cluster";

  // Health gauge
  const health = s?.health;
  const healthScore = health?.score ?? 0;
  const healthStatus = health?.status ?? "unknown";
  const healthColor = healthStatusColor(healthStatus);
  const healthLabel = healthStatus === "unknown"
    ? "UNKNOWN"
    : healthStatus.toUpperCase();

  // Workloads degraded: approximate from pods failed
  const workloadsDegraded = s?.pods.failed ?? 0;
  const criticalAlerts = s?.alerts.critical ?? 0;
  const nodesReady = s?.nodes.ready ?? 0;

  // Metric tiles: CPU%, Memory%, Pods, Network I/O
  const cpuPct = Math.round(s?.cpu?.percentage ?? 0);
  const memPct = Math.round(s?.memory?.percentage ?? 0);

  // p95 over the selected window, computed from the trend series (which is
  // already scoped to the active time-range tab).
  const netRxP95 = percentile(t?.networkRx, 95);
  const netTxP95 = percentile(t?.networkTx, 95);

  // Delta: compare last vs second-to-last in trend series (null when unavailable)
  function lastDelta(series: number[] | null | undefined): number | null {
    if (!series || series.length < 2) return null;
    const last = series[series.length - 1];
    const prev = series[series.length - 2];
    if (!Number.isFinite(last) || !Number.isFinite(prev) || prev === 0) {
      return null;
    }
    return Math.round(((last - prev) / prev) * 100);
  }

  const cpuDelta = lastDelta(t?.cpu);
  const memDelta = lastDelta(t?.memory);
  const podDelta = lastDelta(t?.pods);

  // Pod donut segments
  const podRunning = s?.pods.running ?? 0;
  const podPending = s?.pods.pending ?? 0;
  const podFailed = s?.pods.failed ?? 0;
  const podTotal = podCount || 1;
  const donutSegments: DonutSegment[] = podTotal > 0
    ? [
      {
        value: podRunning,
        color: "var(--success)",
        label: "Running",
      },
      {
        value: podPending,
        color: "var(--warning)",
        label: "Pending",
      },
      {
        value: podFailed,
        color: "var(--error)",
        label: "Failed",
      },
    ]
    : [{ value: 1, color: "var(--border-subtle)" }];

  // Synced label
  const syncedLabel = syncedAgo.value ? `synced ${syncedAgo.value}` : "";
  const subtitleParts = [
    clusterName,
    `${nodeCount} node${nodeCount !== 1 ? "s" : ""}`,
    `${podCount} pods`,
    ...(syncedLabel ? [syncedLabel] : []),
  ].join(" · ");

  return (
    <div>
      {/* ── PAGE HEADER ─────────────────────────────────────────────────────── */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          marginBottom: "20px",
          gap: "16px",
          flexWrap: "wrap",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
            }}
          >
            Cluster Overview
          </h1>
          <div
            style={{
              fontSize: "13px",
              color: "var(--text-muted)",
              marginTop: "4px",
            }}
          >
            {subtitleParts}
          </div>
        </div>

        {/* Time-range segmented tabs */}
        <div
          style={{
            display: "flex",
            gap: "2px",
            background: "var(--glass-surface)",
            border: "1px solid var(--glass-border)",
            borderRadius: "8px",
            padding: "3px",
          }}
        >
          {TIME_RANGES.map((r) => (
            <button
              key={r}
              type="button"
              onClick={() => {
                if (timeRange.value === r) return;
                timeRange.value = r;
                fetchTrendsForTab(r);
              }}
              style={{
                padding: "5px 12px",
                borderRadius: "6px",
                border: "none",
                cursor: "pointer",
                fontSize: "12px",
                fontWeight: 500,
                background: timeRange.value === r
                  ? "var(--accent)"
                  : "transparent",
                color: timeRange.value === r
                  ? "var(--bg-base)"
                  : "var(--text-muted)",
                transition: "background 0.15s, color 0.15s",
              }}
            >
              {r}
            </button>
          ))}
        </div>
      </div>

      {/* ── ROW 1: HEALTH + METRIC TILES ────────────────────────────────────── */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "var(--grid-gap, 16px)",
          marginBottom: "var(--grid-gap, 16px)",
        }}
      >
        {/* Cluster Health card */}
        <div style={{ flex: "2 1 320px", minWidth: "280px" }}>
          <WidgetShell title="Cluster Health">
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "24px",
                flexWrap: "wrap",
              }}
            >
              {/* Gauge ring */}
              <div style={{ flexShrink: 0 }}>
                <Gauge
                  value={healthScore}
                  size={140}
                  thickness={12}
                  color={healthColor}
                  label={`${healthScore}`}
                  sublabel={healthLabel}
                />
              </div>

              {/* Checklist */}
              <div style={{ flex: 1, minWidth: "160px" }}>
                <CheckItem
                  label="Nodes ready"
                  value={`${nodesReady} / ${nodeCount}`}
                  status={nodesReady === nodeCount && nodeCount > 0
                    ? "success"
                    : "warning"}
                />
                <CheckItem
                  label="Workloads degraded"
                  value={workloadsDegraded > 0
                    ? String(workloadsDegraded)
                    : "0"}
                  status={workloadsDegraded > 0 ? "warning" : "success"}
                />
                <CheckItem
                  label="Critical alerts"
                  value={criticalAlerts > 0 ? String(criticalAlerts) : "0"}
                  status={criticalAlerts > 0 ? "error" : "success"}
                />
              </div>
            </div>
          </WidgetShell>
        </div>

        {/* 2×2 Metric tile grid */}
        <div
          style={{
            flex: "3 1 380px",
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "var(--grid-gap, 16px)",
          }}
        >
          <MetricTile
            label="CPU"
            value={`${cpuPct}`}
            unit="%"
            delta={cpuDelta}
            sparkData={t?.cpu}
            sparkColor="var(--accent)"
            href="/cluster/nodes"
          />
          <MetricTile
            label="Memory"
            value={`${memPct}`}
            unit="%"
            delta={memDelta}
            sparkData={t?.memory}
            sparkColor="var(--accent-secondary)"
            href="/cluster/nodes"
          />
          <MetricTile
            label="Pods"
            value={String(podCount)}
            unit={s ? `/${s.pods.running} running` : ""}
            delta={podDelta}
            sparkData={t?.pods}
            sparkColor="var(--success)"
            href="/workloads/pods"
          />
          <NetworkTile
            rxP95={netRxP95}
            txP95={netTxP95}
            rxData={t?.networkRx}
            txData={t?.networkTx}
            period={trendsPeriod.value}
            href="/cluster/nodes"
          />
        </div>
      </div>

      {/* ── ROW 2: RESOURCE UTILIZATION CHART + POD STATUS DONUT ────────────── */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "var(--grid-gap, 16px)",
          marginBottom: "var(--grid-gap, 16px)",
        }}
      >
        {/* Resource utilization area chart */}
        <div style={{ flex: "3 1 380px", minWidth: "280px" }}>
          <WidgetShell
            title="Resource Utilization"
            action={
              <div
                style={{ display: "flex", alignItems: "center", gap: "12px" }}
              >
                <span
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "5px",
                    fontSize: "12px",
                    color: "var(--text-muted)",
                  }}
                >
                  <span
                    style={{
                      display: "inline-block",
                      width: "8px",
                      height: "8px",
                      borderRadius: "2px",
                      background: "var(--accent)",
                    }}
                  />
                  CPU
                </span>
                <span
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "5px",
                    fontSize: "12px",
                    color: "var(--text-muted)",
                  }}
                >
                  <span
                    style={{
                      display: "inline-block",
                      width: "8px",
                      height: "8px",
                      borderRadius: "2px",
                      background: "var(--accent-secondary)",
                    }}
                  />
                  Memory
                </span>
              </div>
            }
          >
            <ResourceAreaChart
              cpuData={t?.cpu ?? null}
              memData={t?.memory ?? null}
            />
          </WidgetShell>
        </div>

        {/* Pod status donut */}
        <div style={{ flex: "2 1 240px", minWidth: "200px" }}>
          <WidgetShell title="Pod Status">
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "20px",
                flexWrap: "wrap",
              }}
            >
              <Donut
                segments={donutSegments}
                size={112}
                thickness={18}
                center={
                  <div
                    style={{
                      display: "flex",
                      flexDirection: "column",
                      alignItems: "center",
                    }}
                  >
                    <span
                      style={{
                        fontSize: "22px",
                        fontWeight: 750,
                        color: "var(--text-primary)",
                        lineHeight: 1,
                      }}
                    >
                      {podCount}
                    </span>
                    <span
                      style={{
                        fontSize: "10px",
                        color: "var(--text-muted)",
                        marginTop: "2px",
                      }}
                    >
                      pods
                    </span>
                  </div>
                }
              />
              {/* Legend */}
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "8px",
                  flex: 1,
                }}
              >
                {[
                  {
                    label: "Running",
                    value: podRunning,
                    color: "var(--success)",
                  },
                  {
                    label: "Pending",
                    value: podPending,
                    color: "var(--warning)",
                  },
                  {
                    label: "Failed",
                    value: podFailed,
                    color: "var(--error)",
                  },
                ].map(({ label, value, color }) => (
                  <div
                    key={label}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "space-between",
                      gap: "8px",
                    }}
                  >
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: "6px",
                      }}
                    >
                      <span
                        style={{
                          width: "8px",
                          height: "8px",
                          borderRadius: "50%",
                          background: color,
                          flexShrink: 0,
                        }}
                      />
                      <span
                        style={{
                          fontSize: "12px",
                          color: "var(--text-secondary)",
                        }}
                      >
                        {label}
                      </span>
                    </div>
                    <span
                      style={{
                        fontSize: "13px",
                        fontWeight: 600,
                        color: "var(--text-primary)",
                        fontFamily: "var(--font-mono, monospace)",
                      }}
                    >
                      {value}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          </WidgetShell>
        </div>
      </div>

      {/* ── ROW 3: NODES + RECENT EVENTS + ACTIVE ALERTS ────────────────────── */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "var(--grid-gap, 16px)",
        }}
      >
        {/* Nodes BarRow card */}
        <div style={{ flex: "2 1 260px", minWidth: "220px" }}>
          <WidgetShell
            title="Nodes"
            action={nodeCount > 0
              ? (
                <span
                  style={{
                    fontSize: "12px",
                    color: nodesReady === nodeCount
                      ? "var(--success)"
                      : "var(--warning)",
                  }}
                >
                  {nodesReady} ready{nodeCount > nodesReady
                    ? ` · ${nodeCount - nodesReady} under pressure`
                    : ""}
                </span>
              )
              : undefined}
          >
            {nodeCount === 0
              ? (
                <div
                  style={{
                    color: "var(--text-muted)",
                    fontSize: "13px",
                    textAlign: "center",
                    padding: "24px 0",
                  }}
                >
                  No node data available
                </div>
              )
              : (
                <div>
                  <BarRow
                    label="CPU"
                    value={cpuPct}
                    max={100}
                    suffix={`${cpuPct}%`}
                    color="var(--accent)"
                  />
                  <BarRow
                    label="Memory"
                    value={memPct}
                    max={100}
                    suffix={`${memPct}%`}
                    color="var(--accent-secondary)"
                  />
                  <BarRow
                    label="Pods"
                    value={podCount}
                    max={Math.max(podCount, 440)}
                    suffix={String(podCount)}
                    color="var(--success)"
                  />
                  {/* Node readiness bar */}
                  <BarRow
                    label="Ready"
                    value={nodesReady}
                    max={nodeCount || 1}
                    suffix={`${nodesReady}/${nodeCount}`}
                    color={nodesReady === nodeCount
                      ? "var(--success)"
                      : "var(--warning)"}
                  />
                </div>
              )}
            <a
              href="/cluster/nodes"
              style={{
                display: "block",
                marginTop: "12px",
                fontSize: "12px",
                color: "var(--accent)",
                textDecoration: "none",
              }}
            >
              View all nodes →
            </a>
          </WidgetShell>
        </div>

        {/* Recent Events */}
        <div style={{ flex: "3 1 300px", minWidth: "240px" }}>
          <WidgetShell
            title="Recent Events"
            action={events.value.length > 0
              ? (
                <a
                  href="/cluster/events"
                  style={{
                    fontSize: "12px",
                    color: "var(--accent)",
                    textDecoration: "none",
                  }}
                >
                  View all →
                </a>
              )
              : undefined}
          >
            {events.value.length === 0
              ? (
                <div
                  style={{
                    color: "var(--text-muted)",
                    fontSize: "12px",
                    textAlign: "center",
                    padding: "24px 0",
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
              : (
                <div
                  style={{
                    display: "flex",
                    flexDirection: "column",
                    gap: "2px",
                  }}
                >
                  {events.value.map((evt, idx) => {
                    const isWarning = evt.type === "Warning";
                    const kind = evt.involvedObject?.kind?.toLowerCase() ?? "";
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
                        style={{
                          display: "flex",
                          alignItems: "flex-start",
                          gap: "10px",
                          padding: "7px 8px",
                          borderRadius: "var(--radius-sm)",
                        }}
                      >
                        <span
                          style={{
                            width: "7px",
                            height: "7px",
                            borderRadius: "50%",
                            marginTop: "4px",
                            flexShrink: 0,
                            background: isWarning
                              ? "var(--warning)"
                              : "var(--accent)",
                          }}
                        />
                        <div style={{ flex: 1, minWidth: 0 }}>
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
                          <div
                            style={{
                              fontSize: "10px",
                              color: "var(--text-muted)",
                              marginTop: "2px",
                              fontFamily: "var(--font-mono, monospace)",
                            }}
                          >
                            {[
                              evt.source?.component,
                              evt.involvedObject?.namespace,
                            ]
                              .filter(Boolean)
                              .join(" · ")}
                          </div>
                        </div>
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
              )}
          </WidgetShell>
        </div>

        {/* Active Alerts */}
        <div style={{ flex: "2 1 240px", minWidth: "200px" }}>
          <WidgetShell
            title="Active Alerts"
            action={(s?.alerts.active ?? 0) > 0
              ? (
                <span
                  style={{
                    fontSize: "11px",
                    fontWeight: 600,
                    padding: "3px 8px",
                    borderRadius: "10px",
                    background: criticalAlerts > 0
                      ? "var(--error-dim)"
                      : "var(--warning-dim)",
                    color: criticalAlerts > 0
                      ? "var(--error)"
                      : "var(--warning)",
                  }}
                >
                  {s?.alerts.active ?? 0} firing
                </span>
              )
              : undefined}
          >
            {(s?.alerts.active ?? 0) === 0
              ? (
                <div
                  style={{
                    display: "flex",
                    flexDirection: "column",
                    alignItems: "center",
                    gap: "8px",
                    padding: "24px 0",
                    color: "var(--text-muted)",
                    fontSize: "12px",
                    textAlign: "center",
                  }}
                >
                  <svg
                    width="20"
                    height="20"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="var(--success)"
                    stroke-width="2"
                  >
                    <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
                    <polyline points="22 4 12 14.01 9 11.01" />
                  </svg>
                  <span>All clear</span>
                </div>
              )
              : (
                <div
                  style={{
                    display: "flex",
                    flexDirection: "column",
                    gap: "2px",
                  }}
                >
                  {/* Summary line */}
                  {criticalAlerts > 0 && (
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: "8px",
                        padding: "7px 0",
                        borderBottom: "1px solid var(--glass-border)",
                        marginBottom: "6px",
                      }}
                    >
                      <span
                        style={{
                          width: "7px",
                          height: "7px",
                          borderRadius: "50%",
                          background: "var(--error)",
                          flexShrink: 0,
                        }}
                      />
                      <span
                        style={{
                          fontSize: "13px",
                          color: "var(--error)",
                          fontWeight: 600,
                        }}
                      >
                        {criticalAlerts} critical
                      </span>
                    </div>
                  )}
                  <div
                    style={{
                      fontSize: "12px",
                      color: "var(--text-muted)",
                      marginTop: "8px",
                    }}
                  >
                    {(s?.alerts.active ?? 0) - criticalAlerts > 0 && (
                      <span>
                        +{(s?.alerts.active ?? 0) - criticalAlerts} warning
                        {(s?.alerts.active ?? 0) - criticalAlerts !== 1
                          ? "s"
                          : ""}
                      </span>
                    )}
                  </div>
                  <a
                    href="/alerting"
                    style={{
                      display: "block",
                      marginTop: "12px",
                      fontSize: "12px",
                      color: "var(--accent)",
                      textDecoration: "none",
                    }}
                  >
                    View all alerts →
                  </a>
                </div>
              )}
          </WidgetShell>
        </div>
      </div>
    </div>
  );
}
