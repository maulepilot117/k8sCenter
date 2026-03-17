import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";

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
  resultType: string;
  result: MetricSeries[];
}

/** PromQL templates per resource kind */
const QUERIES: Record<string, { title: string; query: string }[]> = {
  deployments: [
    {
      title: "CPU Usage (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}",pod=~"{name}-.*",container!=""}[5m]))',
    },
    {
      title: "Memory Usage (MB)",
      query:
        'sum(container_memory_working_set_bytes{namespace="{namespace}",pod=~"{name}-.*",container!=""}) / 1024 / 1024',
    },
    {
      title: "Network Rx (KB/s)",
      query:
        'sum(rate(container_network_receive_bytes_total{namespace="{namespace}",pod=~"{name}-.*"}[5m])) / 1024',
    },
    {
      title: "Network Tx (KB/s)",
      query:
        'sum(rate(container_network_transmit_bytes_total{namespace="{namespace}",pod=~"{name}-.*"}[5m])) / 1024',
    },
    {
      title: "Replicas",
      query:
        'kube_deployment_status_replicas{namespace="{namespace}",deployment="{name}"}',
    },
    {
      title: "Unavailable Replicas",
      query:
        'kube_deployment_status_replicas_unavailable{namespace="{namespace}",deployment="{name}"}',
    },
  ],
  pods: [
    {
      title: "CPU Usage (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}",pod="{name}",container!=""}[5m]))',
    },
    {
      title: "Memory Usage (MB)",
      query:
        'sum(container_memory_working_set_bytes{namespace="{namespace}",pod="{name}",container!=""}) / 1024 / 1024',
    },
    {
      title: "Network Rx (KB/s)",
      query:
        'sum(rate(container_network_receive_bytes_total{namespace="{namespace}",pod="{name}"}[5m])) / 1024',
    },
    {
      title: "Network Tx (KB/s)",
      query:
        'sum(rate(container_network_transmit_bytes_total{namespace="{namespace}",pod="{name}"}[5m])) / 1024',
    },
    {
      title: "Container Restarts",
      query:
        'sum(kube_pod_container_status_restarts_total{namespace="{namespace}",pod="{name}"})',
    },
    {
      title: "Cilium Policy Drops",
      query:
        'sum(rate(hubble_drop_total{reason="Policy denied",destination=~"{namespace}/{name}"}[5m]))',
    },
  ],
  nodes: [
    {
      title: "CPU Usage %",
      query:
        '100 - (avg(rate(node_cpu_seconds_total{mode="idle",instance=~"{nodeInstance}"}[5m])) * 100)',
    },
    {
      title: "Memory Usage %",
      query:
        '100 * (1 - node_memory_MemAvailable_bytes{instance=~"{nodeInstance}"} / node_memory_MemTotal_bytes{instance=~"{nodeInstance}"})',
    },
    {
      title: "Load Average (5m)",
      query: 'node_load5{instance=~"{nodeInstance}"}',
    },
    {
      title: "Network Rx (MB/s)",
      query:
        'sum(rate(node_network_receive_bytes_total{instance=~"{nodeInstance}",device!~"veth.*|cali.*|lxc.*|cilium.*"}[5m])) / 1024 / 1024',
    },
    {
      title: "Network Tx (MB/s)",
      query:
        'sum(rate(node_network_transmit_bytes_total{instance=~"{nodeInstance}",device!~"veth.*|cali.*|lxc.*|cilium.*"}[5m])) / 1024 / 1024',
    },
    {
      title: "Disk Read (MB/s)",
      query:
        'sum(rate(node_disk_read_bytes_total{instance=~"{nodeInstance}"}[5m])) / 1024 / 1024',
    },
    {
      title: "Disk Write (MB/s)",
      query:
        'sum(rate(node_disk_written_bytes_total{instance=~"{nodeInstance}"}[5m])) / 1024 / 1024',
    },
  ],
  statefulsets: [
    {
      title: "CPU Usage (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}",pod=~"{name}-.*",container!=""}[5m]))',
    },
    {
      title: "Memory Usage (MB)",
      query:
        'sum(container_memory_working_set_bytes{namespace="{namespace}",pod=~"{name}-.*",container!=""}) / 1024 / 1024',
    },
    {
      title: "Network Rx (KB/s)",
      query:
        'sum(rate(container_network_receive_bytes_total{namespace="{namespace}",pod=~"{name}-.*"}[5m])) / 1024',
    },
    {
      title: "Network Tx (KB/s)",
      query:
        'sum(rate(container_network_transmit_bytes_total{namespace="{namespace}",pod=~"{name}-.*"}[5m])) / 1024',
    },
    {
      title: "Ready Replicas",
      query:
        'kube_statefulset_status_replicas_ready{namespace="{namespace}",statefulset="{name}"}',
    },
  ],
  daemonsets: [
    {
      title: "CPU Usage (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}",pod=~"{name}-.*",container!=""}[5m]))',
    },
    {
      title: "Memory Usage (MB)",
      query:
        'sum(container_memory_working_set_bytes{namespace="{namespace}",pod=~"{name}-.*",container!=""}) / 1024 / 1024',
    },
    {
      title: "Network Rx (KB/s)",
      query:
        'sum(rate(container_network_receive_bytes_total{namespace="{namespace}",pod=~"{name}-.*"}[5m])) / 1024',
    },
    {
      title: "Ready / Desired",
      query:
        'kube_daemonset_status_number_ready{namespace="{namespace}",daemonset="{name}"}',
    },
  ],
  replicasets: [
    {
      title: "CPU Usage (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}",pod=~"{name}-.*",container!=""}[5m]))',
    },
    {
      title: "Memory Usage (MB)",
      query:
        'sum(container_memory_working_set_bytes{namespace="{namespace}",pod=~"{name}-.*",container!=""}) / 1024 / 1024',
    },
  ],
  jobs: [
    {
      title: "CPU Usage (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}",pod=~"{name}-.*",container!=""}[5m]))',
    },
    {
      title: "Memory Usage (MB)",
      query:
        'sum(container_memory_working_set_bytes{namespace="{namespace}",pod=~"{name}-.*",container!=""}) / 1024 / 1024',
    },
  ],
  cronjobs: [
    {
      title: "Last Job CPU (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}",pod=~"{name}-.*",container!=""}[5m]))',
    },
    {
      title: "Last Job Memory (MB)",
      query:
        'sum(container_memory_working_set_bytes{namespace="{namespace}",pod=~"{name}-.*",container!=""}) / 1024 / 1024',
    },
  ],
  services: [
    {
      title: "Endpoint Pods CPU (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}",pod=~".*",container!=""}[5m])) by (pod)',
    },
  ],
  pvcs: [
    {
      title: "Volume Usage (GiB)",
      query:
        'kubelet_volume_stats_used_bytes{namespace="{namespace}",persistentvolumeclaim="{name}"} / 1024 / 1024 / 1024',
    },
    {
      title: "Volume Capacity (GiB)",
      query:
        'kubelet_volume_stats_capacity_bytes{namespace="{namespace}",persistentvolumeclaim="{name}"} / 1024 / 1024 / 1024',
    },
    {
      title: "Inodes Used",
      query:
        'kubelet_volume_stats_inodes_used{namespace="{namespace}",persistentvolumeclaim="{name}"}',
    },
  ],
  hpas: [
    {
      title: "Current Replicas",
      query:
        'kube_horizontalpodautoscaler_status_current_replicas{namespace="{namespace}",horizontalpodautoscaler="{name}"}',
    },
    {
      title: "Desired Replicas",
      query:
        'kube_horizontalpodautoscaler_status_desired_replicas{namespace="{namespace}",horizontalpodautoscaler="{name}"}',
    },
  ],
  ingresses: [
    {
      title: "Request Rate (req/s)",
      query:
        'sum(rate(nginx_ingress_controller_requests{namespace="{namespace}",ingress="{name}"}[5m]))',
    },
    {
      title: "Error Rate (5xx/s)",
      query:
        'sum(rate(nginx_ingress_controller_requests{namespace="{namespace}",ingress="{name}",status=~"5.."}[5m]))',
    },
  ],
  namespaces: [
    {
      title: "Total CPU (cores)",
      query:
        'sum(rate(container_cpu_usage_seconds_total{namespace="{name}",container!=""}[5m]))',
    },
    {
      title: "Total Memory (MB)",
      query:
        'sum(container_memory_working_set_bytes{namespace="{name}",container!=""}) / 1024 / 1024',
    },
    {
      title: "Network Rx (KB/s)",
      query:
        'sum(rate(container_network_receive_bytes_total{namespace="{name}"}[5m])) / 1024',
    },
    {
      title: "Network Tx (KB/s)",
      query:
        'sum(rate(container_network_transmit_bytes_total{namespace="{name}"}[5m])) / 1024',
    },
    {
      title: "Pod Count",
      query: 'count(kube_pod_info{namespace="{name}"})',
    },
    {
      title: "Cilium Policy Drops",
      query:
        'sum(rate(hubble_drop_total{reason="Policy denied",destination=~"{name}/.*"}[5m]))',
    },
  ],
  networkpolicies: [
    {
      title: "Cilium Forwarded Flows",
      query:
        'sum(rate(hubble_flows_processed_total{verdict="FORWARDED",destination=~"{namespace}/.*"}[5m]))',
    },
    {
      title: "Cilium Dropped Flows",
      query:
        'sum(rate(hubble_flows_processed_total{verdict="DROPPED",destination=~"{namespace}/.*"}[5m]))',
    },
    {
      title: "Policy Denied Drops",
      query:
        'sum(rate(hubble_drop_total{reason="Policy denied",destination=~"{namespace}/.*"}[5m]))',
    },
    {
      title: "TCP Connections (SYN/s)",
      query:
        'sum(rate(hubble_tcp_flags_total{flag="SYN",destination=~"{namespace}/.*"}[5m]))',
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
    const templates = QUERIES[kind];
    if (!templates) return;

    const now = new Date();
    const end = now.toISOString();
    const start = new Date(now.getTime() - 3600 * 1000).toISOString();
    const step = "60s";

    const initial: ChartData[] = templates.map((t) => ({
      title: t.title,
      values: [],
      loading: true,
      error: null,
    }));
    charts.value = initial;

    // For nodes, resolve the node name to node-exporter instance (internal_ip:9100)
    // kube_node_info has internal_ip label, node-exporter uses instance=IP:9100
    let nodeInstance = "";
    if (kind === "nodes") {
      try {
        const nodeInfoRes = await apiGet<{
          result: { metric: { internal_ip: string } }[];
        }>(
          `/v1/monitoring/query?query=${
            encodeURIComponent(
              `kube_node_info{node="${name}"}`,
            )
          }`,
        );
        const results = nodeInfoRes.data?.result;
        if (results && results.length > 0) {
          const ip = results[0].metric.internal_ip;
          nodeInstance = `${ip}:9100`;
        }
      } catch {
        // Fall back to name-based matching
        nodeInstance = `${name}.*`;
      }
    }

    const results = await Promise.allSettled(
      templates.map(async (t, i) => {
        const query = t.query
          .replaceAll("{namespace}", namespace || "")
          .replaceAll("{name}", name)
          .replaceAll("{nodeInstance}", nodeInstance || `${name}.*`);

        const res = await apiGet<QueryRangeResult>(
          `/v1/monitoring/query_range?query=${
            encodeURIComponent(query)
          }&start=${start}&end=${end}&step=${step}`,
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
        <div class="h-6 w-6 animate-spin rounded-full border-2 border-slate-300 border-t-brand" />
      </div>
    );
  }

  if (!monAvailable.value) {
    return (
      <div class="p-12 text-center text-sm text-slate-400">
        <p class="text-lg font-medium text-slate-500">
          Monitoring Not Available
        </p>
        <p class="mt-2">Prometheus was not detected in this cluster.</p>
      </div>
    );
  }

  if (!QUERIES[kind]) {
    return (
      <div class="p-12 text-center text-sm text-slate-400">
        <p class="text-lg font-medium text-slate-500">No Metrics Available</p>
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
          class="h-4 w-4 text-slate-400"
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
          class="rounded-md border border-slate-300 bg-white px-2 py-1 text-xs text-slate-700 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200"
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
            class="rounded-lg border border-slate-200 p-4 dark:border-slate-700"
          >
            <h3 class="mb-3 text-sm font-medium text-slate-700 dark:text-slate-300">
              {chart.title}
            </h3>
            {chart.loading
              ? (
                <div class="flex h-32 items-center justify-center">
                  <div class="h-5 w-5 animate-spin rounded-full border-2 border-slate-300 border-t-brand" />
                </div>
              )
              : chart.error
              ? (
                <div class="flex h-32 items-center justify-center text-sm text-red-400">
                  {chart.error}
                </div>
              )
              : chart.values.length === 0
              ? (
                <div class="flex h-32 items-center justify-center text-sm text-slate-400">
                  No data
                </div>
              )
              : <MiniChart values={chart.values} />}
          </div>
        ))}
      </div>
    </div>
  );
}

/** Simple SVG sparkline chart */
function MiniChart(
  { values }: { values: { time: Date; value: number }[] },
) {
  if (values.length < 2) return <div class="h-32 text-slate-400">No data</div>;

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
      <div class="mb-1 text-right text-lg font-semibold text-slate-900 dark:text-white">
        {displayValue}
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        class="h-28 w-full"
        preserveAspectRatio="none"
      >
        {/* Area fill */}
        <polygon
          points={`${points[0].split(",")[0]},${height} ${points.join(" ")} ${
            points[points.length - 1].split(",")[0]
          },${height}`}
          fill="url(#gradient)"
          opacity="0.3"
        />
        {/* Line */}
        <polyline
          points={points.join(" ")}
          fill="none"
          stroke="rgb(59, 130, 246)"
          stroke-width="2"
          vector-effect="non-scaling-stroke"
        />
        <defs>
          <linearGradient id="gradient" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="rgb(59, 130, 246)" />
            <stop offset="100%" stop-color="transparent" />
          </linearGradient>
        </defs>
      </svg>
      <div class="mt-1 flex justify-between text-xs text-slate-400">
        <span>{values[0].time.toLocaleTimeString()}</span>
        <span>{values[values.length - 1].time.toLocaleTimeString()}</span>
      </div>
    </div>
  );
}
