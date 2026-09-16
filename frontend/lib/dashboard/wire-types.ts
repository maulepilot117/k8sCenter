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
 * Mirrors the backend payload from GET /v1/cluster/dashboard-trends — short
 * historical series (oldest→newest) that back the metric-card sparklines. Any
 * series may be empty when Prometheus or kube-state-metrics is unavailable;
 * the cards then render no sparkline.
 */
export interface DashboardTrends {
  pods: number[] | null;
  cpu: number[] | null;
  memory: number[] | null;
  // Cluster-wide network throughput in Mbps (oldest→newest). The Network I/O
  // tile derives its displayed RX/TX p95 from these series, so the value tracks
  // whichever time-range tab is active.
  networkRx: number[] | null;
  networkTx: number[] | null;
}
