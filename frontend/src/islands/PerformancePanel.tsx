/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiGet } from "@/lib/api.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

interface PerformancePanelProps {
  kind: string;
  name: string;
  namespace?: string;
}

interface MetricSeries {
  metric: Record<string, string>;
  values: [number, string][];
}

interface QueryRangeResult {
  slug?: string;
  query?: string;
  resultType: string;
  result: MetricSeries[];
}

/** Slug-based metric definitions per resource kind.
 * Each slug maps to a server-owned PromQL template in the backend Registry
 * (backend/internal/monitoring/query_registry.go).
 * The backend enforces RBAC per slug — no raw PromQL is sent from the client.
 * Finding P2-4 of the 2026-05-22 security audit.
 */
const QUERIES: Record<string, { title: string; slug: string }[]> = {
  deployments: [
    { title: "CPU Usage (cores)", slug: "deployments/cpu" },
    { title: "Memory Usage (MB)", slug: "deployments/memory" },
    { title: "Network Rx (KB/s)", slug: "deployments/network-rx" },
    { title: "Network Tx (KB/s)", slug: "deployments/network-tx" },
    { title: "Replicas", slug: "deployments/replicas" },
    { title: "Unavailable Replicas", slug: "deployments/replicas-unavailable" },
    { title: "CPU Request (cores)", slug: "deployments/cpu-request" },
    { title: "Memory Request (MB)", slug: "deployments/memory-request" },
  ],
  pods: [
    { title: "CPU Usage (cores)", slug: "pods/cpu" },
    { title: "Memory Usage (MB)", slug: "pods/memory" },
    { title: "Network Rx (KB/s)", slug: "pods/network-rx" },
    { title: "Network Tx (KB/s)", slug: "pods/network-tx" },
    { title: "Container Restarts", slug: "pods/restarts" },
    { title: "Cilium Policy Drops", slug: "pods/cilium-drops" },
  ],
  nodes: [
    { title: "CPU Usage %", slug: "nodes/cpu" },
    { title: "Memory Usage %", slug: "nodes/memory" },
    { title: "Load Average (5m)", slug: "nodes/load" },
    { title: "Network Rx (MB/s)", slug: "nodes/network-rx" },
    { title: "Network Tx (MB/s)", slug: "nodes/network-tx" },
    { title: "Disk Read (MB/s)", slug: "nodes/disk-read" },
    { title: "Disk Write (MB/s)", slug: "nodes/disk-write" },
  ],
  statefulsets: [
    { title: "CPU Usage (cores)", slug: "statefulsets/cpu" },
    { title: "Memory Usage (MB)", slug: "statefulsets/memory" },
    { title: "Network Rx (KB/s)", slug: "statefulsets/network-rx" },
    { title: "Network Tx (KB/s)", slug: "statefulsets/network-tx" },
    { title: "Ready Replicas", slug: "statefulsets/replicas-ready" },
    { title: "CPU Request (cores)", slug: "statefulsets/cpu-request" },
    { title: "Memory Request (MB)", slug: "statefulsets/memory-request" },
  ],
  daemonsets: [
    { title: "CPU Usage (cores)", slug: "daemonsets/cpu" },
    { title: "Memory Usage (MB)", slug: "daemonsets/memory" },
    { title: "Network Rx (KB/s)", slug: "daemonsets/network-rx" },
    { title: "Ready / Desired", slug: "daemonsets/ready" },
    { title: "CPU Request (cores)", slug: "daemonsets/cpu-request" },
    { title: "Memory Request (MB)", slug: "daemonsets/memory-request" },
  ],
  replicasets: [
    { title: "CPU Usage (cores)", slug: "replicasets/cpu" },
    { title: "Memory Usage (MB)", slug: "replicasets/memory" },
  ],
  jobs: [
    { title: "CPU Usage (cores)", slug: "jobs/cpu" },
    { title: "Memory Usage (MB)", slug: "jobs/memory" },
  ],
  cronjobs: [
    { title: "Last Job CPU (cores)", slug: "cronjobs/cpu" },
    { title: "Last Job Memory (MB)", slug: "cronjobs/memory" },
  ],
  services: [
    { title: "Endpoint Pods CPU (cores)", slug: "services/endpoint-cpu" },
  ],
  storageclasses: [
    { title: "PV Count", slug: "storageclasses/pv-count" },
    { title: "PVC Count", slug: "storageclasses/pvc-count" },
    { title: "Total Provisioned (GiB)", slug: "storageclasses/provisioned" },
    { title: "Total Used (GiB)", slug: "storageclasses/used" },
  ],
  pvs: [
    { title: "Capacity (GiB)", slug: "pvs/capacity" },
    { title: "Phase", slug: "pvs/phase" },
    { title: "Bound PVC Usage (GiB)", slug: "pvs/pvc-usage" },
    { title: "Bound PVC Inodes Used", slug: "pvs/pvc-inodes" },
  ],
  pvcs: [
    { title: "Volume Usage (GiB)", slug: "pvcs/usage" },
    { title: "Volume Capacity (GiB)", slug: "pvcs/capacity" },
    { title: "Inodes Used", slug: "pvcs/inodes" },
  ],
  hpas: [
    { title: "Current Replicas", slug: "hpas/current-replicas" },
    { title: "Desired Replicas", slug: "hpas/desired-replicas" },
  ],
  ingresses: [
    { title: "Request Rate (req/s)", slug: "ingresses/request-rate" },
    { title: "Error Rate (5xx/s)", slug: "ingresses/error-rate" },
  ],
  namespaces: [
    { title: "Total CPU (cores)", slug: "namespaces/cpu" },
    { title: "Total Memory (MB)", slug: "namespaces/memory" },
    { title: "Network Rx (KB/s)", slug: "namespaces/network-rx" },
    { title: "Network Tx (KB/s)", slug: "namespaces/network-tx" },
    { title: "Pod Count", slug: "namespaces/pod-count" },
    { title: "Cilium Policy Drops", slug: "namespaces/cilium-drops" },
  ],
  networkpolicies: [
    {
      title: "Cilium Forwarded Flows",
      slug: "networkpolicies/cilium-forwarded",
    },
    { title: "Cilium Dropped Flows", slug: "networkpolicies/cilium-dropped" },
    { title: "Policy Denied Drops", slug: "networkpolicies/policy-denied" },
    { title: "TCP Connections (SYN/s)", slug: "networkpolicies/tcp-syn" },
  ],
  ciliumnetworkpolicies: [
    {
      title: "Cilium Forwarded Flows",
      slug: "ciliumnetworkpolicies/cilium-forwarded",
    },
    {
      title: "Cilium Dropped Flows",
      slug: "ciliumnetworkpolicies/cilium-dropped",
    },
    {
      title: "Policy Denied Drops",
      slug: "ciliumnetworkpolicies/policy-denied",
    },
    {
      title: "Policy Verdicts",
      slug: "ciliumnetworkpolicies/policy-verdicts",
    },
  ],
  pdbs: [
    { title: "Current Healthy", slug: "pdbs/current-healthy" },
    { title: "Desired Healthy", slug: "pdbs/desired-healthy" },
    { title: "Disruptions Allowed", slug: "pdbs/disruptions-allowed" },
    { title: "Expected Pods", slug: "pdbs/expected-pods" },
  ],
  resourcequotas: [
    { title: "CPU Requests Used", slug: "resourcequotas/cpu-used" },
    { title: "CPU Requests Hard Limit", slug: "resourcequotas/cpu-hard" },
    { title: "Memory Requests Used (MB)", slug: "resourcequotas/memory-used" },
    {
      title: "Memory Requests Hard Limit (MB)",
      slug: "resourcequotas/memory-hard",
    },
    { title: "Pods Used", slug: "resourcequotas/pods-used" },
    { title: "Pods Hard Limit", slug: "resourcequotas/pods-hard" },
  ],
  limitranges: [
    { title: "Default CPU Limit", slug: "limitranges/cpu-default" },
    { title: "Default Memory Limit (MB)", slug: "limitranges/memory-default" },
    { title: "Min CPU Request", slug: "limitranges/cpu-min" },
    { title: "Max CPU Limit", slug: "limitranges/cpu-max" },
    { title: "Min Memory Request (MB)", slug: "limitranges/memory-min" },
    { title: "Max Memory Limit (MB)", slug: "limitranges/memory-max" },
  ],
  endpoints: [
    { title: "Available Addresses", slug: "endpoints/available" },
    { title: "Not Ready Addresses", slug: "endpoints/not-ready" },
  ],
  endpointslices: [
    { title: "Ready Endpoints", slug: "endpointslices/ready" },
    { title: "Serving Endpoints", slug: "endpointslices/serving" },
    { title: "Terminating Endpoints", slug: "endpointslices/terminating" },
  ],
  validatingwebhookconfigurations: [
    {
      title: "Admission Latency (p99, ms)",
      slug: "validatingwebhookconfigurations/latency-p99",
    },
    {
      title: "Request Rate (req/s)",
      slug: "validatingwebhookconfigurations/request-rate",
    },
    {
      title: "Rejection Rate (req/s)",
      slug: "validatingwebhookconfigurations/rejection-rate",
    },
  ],
  mutatingwebhookconfigurations: [
    {
      title: "Admission Latency (p99, ms)",
      slug: "mutatingwebhookconfigurations/latency-p99",
    },
    {
      title: "Request Rate (req/s)",
      slug: "mutatingwebhookconfigurations/request-rate",
    },
    {
      title: "Rejection Rate (req/s)",
      slug: "mutatingwebhookconfigurations/rejection-rate",
    },
  ],
};

export function hasPerformanceMetrics(kind: string): boolean {
  return Object.hasOwn(QUERIES, kind);
}

const HUBBLE_HINT =
  "These charts read Hubble flow metrics by destination namespace. Prometheus has none for this namespace: Hubble metrics need a destination context (for example flow:destinationContext=namespace) for this view to have data.";

/** Shown in place of the charts when every query came back empty, for kinds
 * whose metrics depend on something a cluster may not export. */
const EMPTY_HINTS: Record<string, string> = {
  networkpolicies: HUBBLE_HINT,
  ciliumnetworkpolicies: HUBBLE_HINT,
  endpoints:
    "These charts count the addresses in the Service's EndpointSlices, from kube-state-metrics. Prometheus has no EndpointSlice data for this object.",
};

interface Resource {
  kind: string;
  name: string;
  namespace?: string;
}

interface ChartData {
  title: string;
  values: { time: Date; value: number }[];
  loading: boolean;
  error: string | null;
}

const REFRESH_OPTIONS = [
  { label: "5s", value: 5_000 },
  { label: "10s", value: 10_000 },
  { label: "30s", value: 30_000 },
  { label: "1m", value: 60_000 },
  { label: "5m", value: 300_000 },
  { label: "Off", value: 0 },
];

export default function PerformancePanel({
  kind,
  name,
  namespace,
}: PerformancePanelProps) {
  const isClient = useIsClient();
  const [charts, setCharts, chartsRef] = useStateRef<ChartData[]>([]);
  const [monAvailable, setMonAvailable] = useState<boolean | null>(null);
  const [refreshInterval, setRefreshInterval, refreshIntervalRef] =
    useStateRef(30_000);
  const intervalRef = useRef<ReturnType<typeof setInterval> | undefined>(
    undefined,
  );
  // Bumped whenever the resource changes or the panel unmounts: a response
  // that arrives after that belongs to a resource no longer on screen.
  const generationRef = useRef(0);
  const activeRef = useRef<{ resource: Resource; generation: number } | null>(
    null,
  );
  const startInterval = useCallback(
    (resource: Resource, generation: number) => {
      if (intervalRef.current) clearInterval(intervalRef.current);
      if (refreshIntervalRef.current > 0) {
        intervalRef.current = setInterval(
          () => loadMetricsRef.current(resource, generation),
          refreshIntervalRef.current,
        );
      }
    },
    [],
  );

  useEffect(() => {
    const resource = { kind, name, namespace };
    // Another resource: none of the charts on screen are its data.
    setCharts([]);
    const generation = ++generationRef.current;
    activeRef.current = { resource, generation };

    // Check monitoring availability, then start auto-refresh
    apiGet<{ prometheus: { available: boolean } }>("/v1/monitoring/status")
      .then((res) => {
        if (generation !== generationRef.current) return;
        setMonAvailable(res.data.prometheus.available);
        if (res.data.prometheus.available) {
          loadMetricsRef.current(resource, generation);
          startInterval(resource, generation);
        }
      })
      .catch(() => {
        if (generation !== generationRef.current) return;
        setMonAvailable(false);
      });

    return () => {
      generationRef.current++;
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [kind, name, namespace, startInterval]);

  async function loadMetrics(
    { kind, name, namespace }: Resource,
    generation: number,
  ) {
    const slugDefs = QUERIES[kind];
    if (!slugDefs || generation !== generationRef.current) return;

    const now = new Date();
    const end = now.toISOString();
    const start = new Date(now.getTime() - 3600 * 1000).toISOString();
    const step = "60s";

    const initial: ChartData[] = slugDefs.map((t) => ({
      title: t.title,
      values: [],
      loading: true,
      error: null,
    }));
    // Spinners only on the first load. A refresh keeps the last results on
    // screen until the new ones arrive, so the charts (or the "No Metric
    // Data" message) don't blink every refresh interval.
    if (chartsRef.current.length === 0) setCharts(initial);

    // For nodes, resolve the internal IP so the backend slug can match
    // node-exporter metrics (which use instance=IP:9100, not node name).
    // Uses the RBAC-gated slug endpoint instead of raw PromQL.
    let nodeInstance = "";
    if (kind === "nodes") {
      try {
        const nodeInfoRes = await apiGet<QueryRangeResult>(
          `/v1/monitoring/queries/nodes/info?name=${encodeURIComponent(name)}`,
        );
        const results = nodeInfoRes.data?.result;
        if (results && results.length > 0) {
          const ip = results[0].metric?.internal_ip;
          if (ip) nodeInstance = `${ip}:9100`;
        }
      } catch {
        // Fall back to name-based matching — node-exporter may use the node name
        nodeInstance = `${name}`;
      }
    }

    // Determine the effective name param for node slugs: use the resolved
    // node-exporter instance (IP:9100) when available, otherwise the node name.
    const effectiveName =
      kind === "nodes" && nodeInstance ? nodeInstance : name;

    const results = await Promise.allSettled(
      slugDefs.map(async (t, i) => {
        // For namespaces, the name IS the namespace — pass as name param only.
        const params = new URLSearchParams({ start, end, step });
        if (kind === "namespaces") {
          params.set("name", name);
        } else {
          params.set("name", effectiveName);
          if (namespace) params.set("namespace", namespace);
        }

        const res = await apiGet<QueryRangeResult>(
          `/v1/monitoring/queries/${t.slug}?${params}`,
        );

        const values =
          res.data.result?.[0]?.values?.map(([ts, val]: [number, string]) => ({
            time: new Date(ts * 1000),
            value: parseFloat(val),
          })) || [];

        return { index: i, values };
      }),
    );

    const updated = [...initial];
    for (const r of results) {
      if (r.status === "fulfilled") {
        updated[r.value.index] = {
          ...updated[r.value.index],
          values: r.value.values,
          loading: false,
        };
      } else {
        const idx = results.indexOf(r);
        updated[idx] = {
          ...updated[idx],
          loading: false,
          error: "Query failed",
        };
      }
    }
    // The resource changed while the queries were in flight.
    if (generation !== generationRef.current) return;
    setCharts(updated);
  }
  const loadMetricsRef = useLatest(loadMetrics);

  if (!isClient) return null;

  if (monAvailable === null) {
    return (
      <div className="flex justify-center p-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  if (!monAvailable) {
    return (
      <div className="p-12 text-center text-sm text-text-muted">
        <p className="text-lg font-medium text-text-muted">
          Monitoring Not Available
        </p>
        <p className="mt-2">Prometheus was not detected in this cluster.</p>
      </div>
    );
  }

  if (!QUERIES[kind]) {
    return (
      <div className="p-12 text-center text-sm text-text-muted">
        <p className="text-lg font-medium text-text-muted">
          No Metrics Available
        </p>
        <p className="mt-1">
          Metrics are not configured for this resource type.
        </p>
      </div>
    );
  }

  // A grid of "No data" cards says nothing the single message below doesn't.
  const allEmpty =
    charts.length > 0 &&
    charts.every((c) => !c.loading && !c.error && c.values.length === 0);

  return (
    <div className="p-4">
      {/* Refresh interval selector */}
      <div className="mb-4 flex items-center justify-end gap-2">
        <svg
          className="h-4 w-4 text-text-muted"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
        >
          <circle cx="8" cy="8" r="6" />
          <path d="M8 4v4l2.5 1.5" />
        </svg>
        <select
          value={refreshInterval}
          onChange={(e) => {
            const next = Number(e.currentTarget.value);
            setRefreshInterval(next);
            const active = activeRef.current;
            if (active) startInterval(active.resource, active.generation);
          }}
          className="rounded-md border border-border-primary bg-surface px-2 py-1 text-xs text-text-secondary text-text-secondary"
        >
          {REFRESH_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </div>
      {allEmpty ? (
        <div className="p-12 text-center text-sm text-text-muted">
          <p className="text-lg font-medium text-text-muted">No Metric Data</p>
          <p className="mt-1">
            {EMPTY_HINTS[kind] ??
              "Prometheus returned no data for this resource in the last hour."}
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {charts.map((chart, i) => (
            <div
              key={chart.title}
              className="rounded-lg border border-border-primary bg-surface p-4"
            >
              <h3 className="mb-3 text-sm font-medium text-text-secondary">
                {chart.title}
              </h3>
              {chart.loading ? (
                <div className="flex h-32 items-center justify-center">
                  <Spinner size="sm" className="text-brand" />
                </div>
              ) : chart.error ? (
                <div className="flex h-32 items-center justify-center text-sm text-error">
                  {chart.error}
                </div>
              ) : chart.values.length === 0 ? (
                <div className="flex h-32 items-center justify-center text-sm text-text-muted">
                  No data
                </div>
              ) : (
                <MiniChart values={chart.values} chartId={`${kind}-${i}`} />
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** Simple SVG sparkline chart */
function MiniChart({
  values,
  chartId,
}: {
  values: { time: Date; value: number }[];
  chartId?: string;
}) {
  if (values.length < 2)
    return <div className="h-32 text-text-muted">No data</div>;

  const width = 400;
  const height = 120;
  const padding = 4;

  const minVal = Math.min(...values.map((v) => v.value));
  const maxVal = Math.max(...values.map((v) => v.value));
  const range = maxVal - minVal || 1;

  const points = values.map((v, i) => {
    const x = padding + (i / (values.length - 1)) * (width - padding * 2);
    const y =
      height - padding - ((v.value - minVal) / range) * (height - padding * 2);
    return `${x},${y}`;
  });

  const currentValue = values[values.length - 1].value;
  const displayValue =
    currentValue < 1
      ? currentValue.toFixed(3)
      : currentValue < 100
        ? currentValue.toFixed(1)
        : Math.round(currentValue).toString();

  return (
    <div>
      <div className="mb-1 text-right text-lg font-semibold text-text-primary">
        {displayValue}
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="h-28 w-full"
        preserveAspectRatio="none"
      >
        <defs>
          <linearGradient
            id={`grad-${chartId ?? "default"}`}
            x1="0"
            y1="0"
            x2="0"
            y2="1"
          >
            <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.3" />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity="0" />
          </linearGradient>
        </defs>
        {/* Area fill */}
        <polygon
          points={`${points[0].split(",")[0]},${height} ${points.join(" ")} ${
            points[points.length - 1].split(",")[0]
          },${height}`}
          fill={`url(#grad-${chartId ?? "default"})`}
        />
        {/* Line */}
        <polyline
          points={points.join(" ")}
          fill="none"
          stroke="var(--accent)"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <div className="mt-1 flex justify-between text-xs text-text-muted">
        <span>{values[0].time.toLocaleTimeString()}</span>
        <span>{values[values.length - 1].time.toLocaleTimeString()}</span>
      </div>
    </div>
  );
}
