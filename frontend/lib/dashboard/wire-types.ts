/**
 * Response shapes for the dashboard's four data sources.
 *
 * These declare only the fields the dashboard actually reads, not the full
 * response bodies. The endpoints return more (cluster info also carries
 * kubernetesVersion and a kubecenter build block; both payloads carry service
 * counts, and trends carries a node series plus its window/step echo) — those
 * are dropped here rather than declared-and-ignored.
 *
 * They live here rather than inside DashboardV2 because every extracted widget
 * needs them and the island is about to shrink to a shell.
 */
import type { ClusterHealth } from "@/lib/score-color.ts";

export interface ClusterInfoData {
  clusterID: string;
  platform: string;
  nodeCount: number;
}

export interface DashboardSummary {
  nodes: { total: number; ready: number };
  pods: { total: number; running: number; pending: number; failed: number };
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

/**
 * One trend series on the response's time grid, oldest first: entry k was
 * sampled at end - window + k * step. A null entry is a step Prometheus had no
 * value for. It keeps its slot so every later entry stays on its own step;
 * skip it, never compact it out, when the position means a time.
 */
export type TrendSeries = ReadonlyArray<number | null>;

/**
 * Mirrors the backend payload from GET /v1/cluster/dashboard-trends — short
 * historical series (oldest→newest) that back the metric-card sparklines. Any
 * series may be null when Prometheus or kube-state-metrics is unavailable;
 * the cards then render no sparkline.
 */
export interface DashboardTrends {
  pods: TrendSeries | null;
  cpu: TrendSeries | null;
  memory: TrendSeries | null;
  // Cluster-wide network throughput in Mbps (oldest→newest). The Network I/O
  // tile derives its displayed RX/TX p95 from these series, so the value tracks
  // whichever time-range tab is active.
  networkRx: TrendSeries | null;
  networkTx: TrendSeries | null;
  // Span and sample spacing of the series above, as Go durations ("1h0m0s",
  // "2m0s"). Optional: older backends omit them and the chart assumes 1h / 2m.
  window?: string;
  step?: string;
  // When the newest grid step was taken, as an RFC 3339 time. Optional: older
  // backends and responses with no query omit it, and the chart then anchors
  // on when the payload arrived.
  end?: string;
}

/**
 * Mirrors the backend payload from GET /v1/diagnostics/{namespace}/summary --
 * every pod in the namespace the backend considers failing, and how many pods
 * it counted in total.
 *
 * `total` is what tells "nothing is wrong here" apart from "there is nothing
 * here", which is the distinction the widget turns on: the handler lists pods
 * out of the informer cache, so a namespace that has been DELETED answers 200
 * with an empty list and a zero total rather than 404. See
 * `rollUpDiagnostics`.
 */
export interface DiagnosticsSummary {
  failing: Array<{ kind: string; name: string; reason: string }>;
  total: number;
}

/**
 * Mirrors GET /v1/resources/counts -- lowercase plural kind to how many the
 * informer cache holds.
 *
 * Two properties of this payload are load-bearing and neither is visible in
 * its type. It is LOCAL-CLUSTER ONLY: the handler refuses a remote cluster
 * context outright, because remote clusters use direct API calls and populate
 * no informers. And a kind the caller has no `list` permission for is OMITTED
 * FROM THE MAP rather than reported as zero -- so a missing key means "not
 * visible to you", never "none of these exist". `rollUpWorkloadHealth` is
 * built entirely around that distinction.
 */
export type ResourceCounts = Record<string, number>;

/**
 * One page of the generic resource list route, `GET /v1/resources/{kind}`.
 *
 * The route answers with the raw Kubernetes objects under `data` and the
 * population size under `metadata.total`, and the two differ: a page is capped
 * at 500 items server-side. Both halves are kept here because a widget that
 * ranked a 500-item page and printed the ranking as the cluster's worst pods
 * would be wrong on exactly the clusters where it matters most.
 *
 * `items` is `unknown[]` deliberately. The consumers read a handful of status
 * fields off objects this build does not control, so they narrow defensively
 * per field rather than asserting a shape the server is free to extend.
 */
export interface ResourceListPage {
  items: unknown[];
  total: number;
}
