import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";

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
    { title: "Cilium Forwarded Flows", slug: "networkpolicies/cilium-forwarded" },
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

export default function PerformancePanel(
  { kind, name, namespace }: PerformancePanelProps,
) {
  const charts = useSignal<ChartData[]>([]);
  const monAvailable = useSignal<boolean | null>(null);
  const refreshInterval = useSignal(30_000);
  const intervalRef = useSignal<number | undefined>(undefined);

  function startInterval() {
    if (intervalRef.value) clearInterval(intervalRef.value);
    if (refreshInterval.value > 0) {
      intervalRef.value = setInterval(loadMetrics, refreshInterval.value);
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;

    // Check monitoring availability, then start auto-refresh
    apiGet<{ prometheus: { available: boolean } }>("/v1/monitoring/status")
      .then((res) => {
        monAvailable.value = res.data.prometheus.available;
        if (res.data.prometheus.available) {
          loadMetrics();
          startInterval();
        }
      })
      .catch(() => {
        monAvailable.value = false;
      });

    return () => {
      if (intervalRef.value) clearInterval(intervalRef.value);
    };
  }, [kind, name, namespace]);

  async function loadMetrics() {
    const slugDefs = QUERIES[kind];
    if (!slugDefs) return;

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
    charts.value = initial;

    // For nodes, resolve the internal IP so the backend slug can match
    // node-exporter metrics (which use instance=IP:9100, not node name).
    // Uses the RBAC-gated slug endpoint instead of raw PromQL.
    let nodeInstance = "";
    if (kind === "nodes") {
      try {
        const nodeInfoRes = await apiGet<QueryRangeResult>(
          `/v1/monitoring/queries/nodes/info?name=${
            encodeURIComponent(name)
          }`,
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
    const effectiveName = kind === "nodes" && nodeInstance
      ? nodeInstance
      : name;

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
    charts.value = updated;
  }

  if (!IS_BROWSER) return null;

  if (monAvailable.value === null) {
    return (
      <div class="flex justify-center p-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (!monAvailable.value) {
    return (
      <div class="p-12 text-center text-sm text-text-muted">
        <p class="text-lg font-medium text-text-muted">
          Monitoring Not Available
        </p>
        <p class="mt-2">Prometheus was not detected in this cluster.</p>
      </div>
    );
  }

  if (!QUERIES[kind]) {
    return (
      <div class="p-12 text-center text-sm text-text-muted">
        <p class="text-lg font-medium text-text-muted">No Metrics Available</p>
        <p class="mt-1">
          Metrics are not configured for this resource type.
        </p>
      </div>
    );
  }

  return (
    <div class="p-4">
      {/* Refresh interval selector */}
      <div class="mb-4 flex items-center justify-end gap-2">
        <svg
          class="h-4 w-4 text-text-muted"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
        >
          <circle cx="8" cy="8" r="6" />
          <path d="M8 4v4l2.5 1.5" />
        </svg>
        <select
          value={refreshInterval.value}
          onChange={(e) => {
            refreshInterval.value = Number(
              (e.target as HTMLSelectElement).value,
            );
            startInterval();
          }}
          class="rounded-md border border-border-primary bg-surface px-2 py-1 text-xs text-text-secondary text-text-secondary"
        >
          {REFRESH_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </div>
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        {charts.value.map((chart, i) => (
          <div
            key={i}
            class="rounded-lg border border-border-primary bg-surface p-4"
          >
            <h3 class="mb-3 text-sm font-medium text-text-secondary">
              {chart.title}
            </h3>
            {chart.loading
              ? (
                <div class="flex h-32 items-center justify-center">
                  <Spinner size="sm" class="text-brand" />
                </div>
              )
              : chart.error
              ? (
                <div class="flex h-32 items-center justify-center text-sm text-error">
                  {chart.error}
                </div>
              )
              : chart.values.length === 0
              ? (
                <div class="flex h-32 items-center justify-center text-sm text-text-muted">
                  No data
                </div>
              )
              : <MiniChart values={chart.values} chartId={`${kind}-${i}`} />}
          </div>
        ))}
      </div>
    </div>
  );
}

/** Simple SVG sparkline chart */
function MiniChart(
  { values, chartId }: {
    values: { time: Date; value: number }[];
    chartId?: string;
  },
) {
  if (values.length < 2) return <div class="h-32 text-text-muted">No data</div>;

  const width = 400;
  const height = 120;
  const padding = 4;

  const minVal = Math.min(...values.map((v) => v.value));
  const maxVal = Math.max(...values.map((v) => v.value));
  const range = maxVal - minVal || 1;

  const points = values.map((v, i) => {
    const x = padding + (i / (values.length - 1)) * (width - padding * 2);
    const y = height - padding -
      ((v.value - minVal) / range) * (height - padding * 2);
    return `${x},${y}`;
  });

  const currentValue = values[values.length - 1].value;
  const displayValue = currentValue < 1
    ? currentValue.toFixed(3)
    : currentValue < 100
    ? currentValue.toFixed(1)
    : Math.round(currentValue).toString();

  return (
    <div>
      <div class="mb-1 text-right text-lg font-semibold text-text-primary">
        {displayValue}
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        class="h-28 w-full"
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
            <stop offset="0%" stop-color="var(--accent)" stop-opacity="0.3" />
            <stop offset="100%" stop-color="var(--accent)" stop-opacity="0" />
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
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          vector-effect="non-scaling-stroke"
        />
      </svg>
      <div class="mt-1 flex justify-between text-xs text-text-muted">
        <span>{values[0].time.toLocaleTimeString()}</span>
        <span>{values[values.length - 1].time.toLocaleTimeString()}</span>
      </div>
    </div>
  );
}
