/** @jsxImportSource react */
import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { Area, AreaChart } from "recharts";
import {
  ChartTooltip,
  TimeXAxis,
  ValueYAxis,
} from "@/components/charts/ChartAxes.tsx";
import ChartFrame from "@/components/charts/ChartFrame.tsx";
import {
  AXIS_CHART_PROPS,
  CHART_TOKENS,
  formatValue,
  HOUR,
  type ValueUnit,
} from "@/components/charts/chart-theme.ts";
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
const MIB = 1024 * 1024;
const GIB = 1024 * MIB;

interface QueryDef {
  title: string;
  slug: string;
  unit: ValueUnit;
  /** Multiplier from the value the backend reports (MB, GiB) to the unit's base (bytes). */
  scale?: number;
}

const QUERIES: Record<string, QueryDef[]> = {
  deployments: [
    { title: "CPU Usage (cores)", slug: "deployments/cpu", unit: "cores" },
    {
      title: "Memory Usage (MB)",
      slug: "deployments/memory",
      unit: "bytes",
      scale: MIB,
    },
    {
      title: "Network Rx (KB/s)",
      slug: "deployments/network-rx",
      unit: "number",
    },
    {
      title: "Network Tx (KB/s)",
      slug: "deployments/network-tx",
      unit: "number",
    },
    { title: "Replicas", slug: "deployments/replicas", unit: "number" },
    {
      title: "Unavailable Replicas",
      slug: "deployments/replicas-unavailable",
      unit: "number",
    },
    {
      title: "CPU Request (cores)",
      slug: "deployments/cpu-request",
      unit: "cores",
    },
    {
      title: "Memory Request (MB)",
      slug: "deployments/memory-request",
      unit: "bytes",
      scale: MIB,
    },
  ],
  pods: [
    { title: "CPU Usage (cores)", slug: "pods/cpu", unit: "cores" },
    {
      title: "Memory Usage (MB)",
      slug: "pods/memory",
      unit: "bytes",
      scale: MIB,
    },
    { title: "Network Rx (KB/s)", slug: "pods/network-rx", unit: "number" },
    { title: "Network Tx (KB/s)", slug: "pods/network-tx", unit: "number" },
    { title: "Container Restarts", slug: "pods/restarts", unit: "number" },
    { title: "Cilium Policy Drops", slug: "pods/cilium-drops", unit: "number" },
  ],
  nodes: [
    { title: "CPU Usage %", slug: "nodes/cpu", unit: "percent" },
    { title: "Memory Usage %", slug: "nodes/memory", unit: "percent" },
    { title: "Load Average (5m)", slug: "nodes/load", unit: "number" },
    { title: "Network Rx (MB/s)", slug: "nodes/network-rx", unit: "number" },
    { title: "Network Tx (MB/s)", slug: "nodes/network-tx", unit: "number" },
    { title: "Disk Read (MB/s)", slug: "nodes/disk-read", unit: "number" },
    { title: "Disk Write (MB/s)", slug: "nodes/disk-write", unit: "number" },
  ],
  statefulsets: [
    { title: "CPU Usage (cores)", slug: "statefulsets/cpu", unit: "cores" },
    {
      title: "Memory Usage (MB)",
      slug: "statefulsets/memory",
      unit: "bytes",
      scale: MIB,
    },
    {
      title: "Network Rx (KB/s)",
      slug: "statefulsets/network-rx",
      unit: "number",
    },
    {
      title: "Network Tx (KB/s)",
      slug: "statefulsets/network-tx",
      unit: "number",
    },
    {
      title: "Ready Replicas",
      slug: "statefulsets/replicas-ready",
      unit: "number",
    },
    {
      title: "CPU Request (cores)",
      slug: "statefulsets/cpu-request",
      unit: "cores",
    },
    {
      title: "Memory Request (MB)",
      slug: "statefulsets/memory-request",
      unit: "bytes",
      scale: MIB,
    },
  ],
  daemonsets: [
    { title: "CPU Usage (cores)", slug: "daemonsets/cpu", unit: "cores" },
    {
      title: "Memory Usage (MB)",
      slug: "daemonsets/memory",
      unit: "bytes",
      scale: MIB,
    },
    {
      title: "Network Rx (KB/s)",
      slug: "daemonsets/network-rx",
      unit: "number",
    },
    { title: "Ready / Desired", slug: "daemonsets/ready", unit: "number" },
    {
      title: "CPU Request (cores)",
      slug: "daemonsets/cpu-request",
      unit: "cores",
    },
    {
      title: "Memory Request (MB)",
      slug: "daemonsets/memory-request",
      unit: "bytes",
      scale: MIB,
    },
  ],
  replicasets: [
    { title: "CPU Usage (cores)", slug: "replicasets/cpu", unit: "cores" },
    {
      title: "Memory Usage (MB)",
      slug: "replicasets/memory",
      unit: "bytes",
      scale: MIB,
    },
  ],
  jobs: [
    { title: "CPU Usage (cores)", slug: "jobs/cpu", unit: "cores" },
    {
      title: "Memory Usage (MB)",
      slug: "jobs/memory",
      unit: "bytes",
      scale: MIB,
    },
  ],
  cronjobs: [
    { title: "Last Job CPU (cores)", slug: "cronjobs/cpu", unit: "cores" },
    {
      title: "Last Job Memory (MB)",
      slug: "cronjobs/memory",
      unit: "bytes",
      scale: MIB,
    },
  ],
  services: [
    {
      title: "Endpoint Pods CPU (cores)",
      slug: "services/endpoint-cpu",
      unit: "cores",
    },
  ],
  storageclasses: [
    { title: "PV Count", slug: "storageclasses/pv-count", unit: "number" },
    { title: "PVC Count", slug: "storageclasses/pvc-count", unit: "number" },
    {
      title: "Total Provisioned (GiB)",
      slug: "storageclasses/provisioned",
      unit: "bytes",
      scale: GIB,
    },
    {
      title: "Total Used (GiB)",
      slug: "storageclasses/used",
      unit: "bytes",
      scale: GIB,
    },
  ],
  pvs: [
    {
      title: "Capacity (GiB)",
      slug: "pvs/capacity",
      unit: "bytes",
      scale: GIB,
    },
    { title: "Phase", slug: "pvs/phase", unit: "number" },
    {
      title: "Bound PVC Usage (GiB)",
      slug: "pvs/pvc-usage",
      unit: "bytes",
      scale: GIB,
    },
    { title: "Bound PVC Inodes Used", slug: "pvs/pvc-inodes", unit: "number" },
  ],
  pvcs: [
    {
      title: "Volume Usage (GiB)",
      slug: "pvcs/usage",
      unit: "bytes",
      scale: GIB,
    },
    {
      title: "Volume Capacity (GiB)",
      slug: "pvcs/capacity",
      unit: "bytes",
      scale: GIB,
    },
    { title: "Inodes Used", slug: "pvcs/inodes", unit: "number" },
  ],
  hpas: [
    {
      title: "Current Replicas",
      slug: "hpas/current-replicas",
      unit: "number",
    },
    {
      title: "Desired Replicas",
      slug: "hpas/desired-replicas",
      unit: "number",
    },
  ],
  ingresses: [
    {
      title: "Request Rate (req/s)",
      slug: "ingresses/request-rate",
      unit: "number",
    },
    {
      title: "Error Rate (5xx/s)",
      slug: "ingresses/error-rate",
      unit: "number",
    },
  ],
  namespaces: [
    { title: "Total CPU (cores)", slug: "namespaces/cpu", unit: "cores" },
    {
      title: "Total Memory (MB)",
      slug: "namespaces/memory",
      unit: "bytes",
      scale: MIB,
    },
    {
      title: "Network Rx (KB/s)",
      slug: "namespaces/network-rx",
      unit: "number",
    },
    {
      title: "Network Tx (KB/s)",
      slug: "namespaces/network-tx",
      unit: "number",
    },
    { title: "Pod Count", slug: "namespaces/pod-count", unit: "number" },
    {
      title: "Cilium Policy Drops",
      slug: "namespaces/cilium-drops",
      unit: "number",
    },
  ],
  networkpolicies: [
    {
      title: "Cilium Forwarded Flows",
      slug: "networkpolicies/cilium-forwarded",
      unit: "number",
    },
    {
      title: "Cilium Dropped Flows",
      slug: "networkpolicies/cilium-dropped",
      unit: "number",
    },
    {
      title: "Policy Denied Drops",
      slug: "networkpolicies/policy-denied",
      unit: "number",
    },
    {
      title: "TCP Connections (SYN/s)",
      slug: "networkpolicies/tcp-syn",
      unit: "number",
    },
  ],
  ciliumnetworkpolicies: [
    {
      title: "Cilium Forwarded Flows",
      slug: "ciliumnetworkpolicies/cilium-forwarded",
      unit: "number",
    },
    {
      title: "Cilium Dropped Flows",
      slug: "ciliumnetworkpolicies/cilium-dropped",
      unit: "number",
    },
    {
      title: "Policy Denied Drops",
      slug: "ciliumnetworkpolicies/policy-denied",
      unit: "number",
    },
    {
      title: "Policy Verdicts",
      slug: "ciliumnetworkpolicies/policy-verdicts",
      unit: "number",
    },
  ],
  pdbs: [
    { title: "Current Healthy", slug: "pdbs/current-healthy", unit: "number" },
    { title: "Desired Healthy", slug: "pdbs/desired-healthy", unit: "number" },
    {
      title: "Disruptions Allowed",
      slug: "pdbs/disruptions-allowed",
      unit: "number",
    },
    { title: "Expected Pods", slug: "pdbs/expected-pods", unit: "number" },
  ],
  resourcequotas: [
    {
      title: "CPU Requests Used",
      slug: "resourcequotas/cpu-used",
      unit: "cores",
    },
    {
      title: "CPU Requests Hard Limit",
      slug: "resourcequotas/cpu-hard",
      unit: "cores",
    },
    {
      title: "Memory Requests Used (MB)",
      slug: "resourcequotas/memory-used",
      unit: "bytes",
      scale: MIB,
    },
    {
      title: "Memory Requests Hard Limit (MB)",
      slug: "resourcequotas/memory-hard",
      unit: "bytes",
      scale: MIB,
    },
    { title: "Pods Used", slug: "resourcequotas/pods-used", unit: "number" },
    {
      title: "Pods Hard Limit",
      slug: "resourcequotas/pods-hard",
      unit: "number",
    },
  ],
  limitranges: [
    {
      title: "Default CPU Limit",
      slug: "limitranges/cpu-default",
      unit: "cores",
    },
    {
      title: "Default Memory Limit (MB)",
      slug: "limitranges/memory-default",
      unit: "bytes",
      scale: MIB,
    },
    { title: "Min CPU Request", slug: "limitranges/cpu-min", unit: "cores" },
    { title: "Max CPU Limit", slug: "limitranges/cpu-max", unit: "cores" },
    {
      title: "Min Memory Request (MB)",
      slug: "limitranges/memory-min",
      unit: "bytes",
      scale: MIB,
    },
    {
      title: "Max Memory Limit (MB)",
      slug: "limitranges/memory-max",
      unit: "bytes",
      scale: MIB,
    },
  ],
  endpoints: [
    {
      title: "Available Addresses",
      slug: "endpoints/available",
      unit: "number",
    },
    {
      title: "Not Ready Addresses",
      slug: "endpoints/not-ready",
      unit: "number",
    },
  ],
  endpointslices: [
    { title: "Ready Endpoints", slug: "endpointslices/ready", unit: "number" },
    {
      title: "Serving Endpoints",
      slug: "endpointslices/serving",
      unit: "number",
    },
    {
      title: "Terminating Endpoints",
      slug: "endpointslices/terminating",
      unit: "number",
    },
  ],
  validatingwebhookconfigurations: [
    {
      title: "Admission Latency (p99, ms)",
      slug: "validatingwebhookconfigurations/latency-p99",
      unit: "number",
    },
    {
      title: "Request Rate (req/s)",
      slug: "validatingwebhookconfigurations/request-rate",
      unit: "number",
    },
    {
      title: "Rejection Rate (req/s)",
      slug: "validatingwebhookconfigurations/rejection-rate",
      unit: "number",
    },
  ],
  mutatingwebhookconfigurations: [
    {
      title: "Admission Latency (p99, ms)",
      slug: "mutatingwebhookconfigurations/latency-p99",
      unit: "number",
    },
    {
      title: "Request Rate (req/s)",
      slug: "mutatingwebhookconfigurations/request-rate",
      unit: "number",
    },
    {
      title: "Rejection Rate (req/s)",
      slug: "mutatingwebhookconfigurations/rejection-rate",
      unit: "number",
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
  unit: ValueUnit;
  start: number;
  end: number;
  values: { time: number; value: number }[];
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
    const endMs = now.getTime();
    const startMs = endMs - HOUR;
    const end = now.toISOString();
    const start = new Date(startMs).toISOString();
    const step = "60s";

    const initial: ChartData[] = slugDefs.map((t) => ({
      title: t.title,
      unit: t.unit,
      start: startMs,
      end: endMs,
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
            time: ts * 1000,
            value: parseFloat(val) * (t.scale ?? 1),
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
          {charts.map((chart) => (
            <div
              key={chart.title}
              className="rounded-lg border border-border-primary bg-surface p-4"
            >
              <h3 className="mb-3 text-sm font-medium text-text-secondary">
                {chart.title}
              </h3>
              {chart.loading ? (
                <div className="flex h-[232px] items-center justify-center">
                  <Spinner size="sm" className="text-brand" />
                </div>
              ) : chart.error ? (
                <div className="flex h-[232px] items-center justify-center text-sm text-error">
                  {chart.error}
                </div>
              ) : chart.values.length === 0 ? (
                <div className="flex h-[232px] items-center justify-center text-sm text-text-muted">
                  No data
                </div>
              ) : (
                <MiniChart chart={chart} />
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** The big readout above a chart: the latest value, to a useful precision. */
function readout(value: number, unit: ValueUnit): string {
  if (unit !== "number") return formatValue(value, unit);
  if (value < 1) return value.toFixed(3);
  return value < 100 ? value.toFixed(1) : Math.round(value).toString();
}

/** One metric over the queried hour, with time and value axes and a tooltip. */
function MiniChart({ chart }: { chart: ChartData }) {
  const gradientId = `area-${useId()}`;
  const { title, unit, start, end, values } = chart;
  const plotted = useMemo(() => values.map((v) => v.value), [values]);
  const latest = values[values.length - 1].value;

  return (
    <div>
      <div className="mb-1 text-right text-lg font-semibold leading-7 text-text-primary">
        {readout(latest, unit)}
      </div>
      <ChartFrame
        preset="axis"
        kind="metric"
        label={`${title} over the last hour`}
      >
        <AreaChart {...AXIS_CHART_PROPS} data={values}>
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop
                offset="0%"
                stopColor={CHART_TOKENS.line}
                stopOpacity={0.3}
              />
              <stop
                offset="100%"
                stopColor={CHART_TOKENS.line}
                stopOpacity={0}
              />
            </linearGradient>
          </defs>
          <TimeXAxis dataKey="time" start={start} end={end} />
          <ValueYAxis unit={unit} values={plotted} />
          <Area
            dataKey="value"
            name={title}
            type="linear"
            stroke={CHART_TOKENS.line}
            strokeWidth={2}
            fill={`url(#${gradientId})`}
            isAnimationActive={false}
            dot={values.length === 1}
          />
          <ChartTooltip unit={unit} rangeMs={end - start} />
        </AreaChart>
      </ChartFrame>
    </div>
  );
}
