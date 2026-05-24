// Per-kind curated PromQL panels for the M4 Metrics tab.
//
// F#4 (security audit 2026-05-22) — panels now reference SERVER-OWNED
// slugs from `backend/internal/monitoring/query_registry.go` rather than
// embedding raw PromQL the mobile app would send through
// `/v1/monitoring/query_range`. After F#23 + F#10 hardening that endpoint
// is admin-only; non-admin operators (the Metrics tab's intended audience)
// were getting 403 on every chart. The slug endpoint
// `/v1/monitoring/queries/{slug}` runs the matching server-side template
// after a per-slug RBAC check, so a viewer can pull pod CPU in namespaces
// they can list pods in and nothing else.
//
// Coverage parity with the web's Grafana dashboards:
//   * pods, nodes — rich coverage (4 panels each)
//   * persistentvolumeclaims, deployments — one canonical panel each
//   * statefulsets, daemonsets — workload-shaped panels using the
//     `<workload>-.*` pod-name matcher the registry expects.
//
// Slug names MUST match a key in the backend Registry exactly — drift is
// silently caught here when the backend returns 404 (which it folds into
// "not found or forbidden" so the client can't enumerate the catalog).

import 'package:flutter/foundation.dart' show immutable;

/// Severity hint the chart layer uses to pick a [KubeColors] token.
/// Mirrors `KubeChartSeverity` exactly — duplicated as a domain enum so
/// the registry doesn't import the widget layer (keeps the registry
/// testable without pulling in Flutter).
enum PanelSeverity { primary, success, warning, error, info, muted }

/// A single chart panel definition. Each panel references a
/// `slug` in the backend's monitoring Registry; the controller hands
/// the slug + (namespace, name) to `MonitoringRepository.queryRangeSlug`,
/// the backend renders + RBAC-checks + executes the PromQL, and the
/// chart layer plots the result.
///
/// F#4 (security audit 2026-05-22) replaced the previous
/// `queryTemplate` / variable-substitution design — that path required
/// hitting `/v1/monitoring/query_range`, which is admin-only after F#23.
@immutable
class MetricPanel {
  const MetricPanel({
    required this.id,
    required this.title,
    required this.slug,
    this.severity = PanelSeverity.primary,
    this.unitHint,
  });

  /// Stable id used by tests and the controller's per-panel state map.
  final String id;

  /// Operator-facing chart title (e.g. "CPU usage per container").
  final String title;

  /// Backend Registry key — e.g. `pods/cpu`, `deployments/replicas`.
  /// Must match a key in
  /// `backend/internal/monitoring/query_registry.go` exactly. Drift
  /// surfaces as a 404 ("not found or forbidden") at runtime.
  final String slug;

  /// Color token for the chart line(s) in this panel.
  final PanelSeverity severity;

  /// Optional unit label rendered above the chart (e.g. "cores", "%",
  /// "bytes/sec"). Not authoritative — Prometheus values arrive
  /// unitless; this is purely operator copy.
  final String? unitHint;
}

/// Curated panels per resource kind. Keys are the backend-canonical
/// plural kind strings (matching the route segment and
/// `ResourceDashboardMap` keys). Slugs reference entries in
/// `backend/internal/monitoring/query_registry.go`.
const Map<String, List<MetricPanel>> metricPanelsByKind = {
  // ---------------- Pod ----------------
  'pods': [
    MetricPanel(
      id: 'pod_cpu_usage',
      title: 'CPU usage',
      slug: 'pods/cpu',
      severity: PanelSeverity.primary,
      unitHint: 'cores',
    ),
    MetricPanel(
      id: 'pod_memory_usage',
      title: 'Memory working set',
      slug: 'pods/memory',
      severity: PanelSeverity.warning,
      unitHint: 'MB',
    ),
    MetricPanel(
      id: 'pod_network_rx',
      title: 'Network receive',
      slug: 'pods/network-rx',
      severity: PanelSeverity.info,
      unitHint: 'KB/s',
    ),
    MetricPanel(
      id: 'pod_network_tx',
      title: 'Network transmit',
      slug: 'pods/network-tx',
      severity: PanelSeverity.success,
      unitHint: 'KB/s',
    ),
  ],

  // ---------------- Node ----------------
  'nodes': [
    MetricPanel(
      id: 'node_cpu_utilization',
      title: 'CPU utilization',
      slug: 'nodes/cpu',
      severity: PanelSeverity.primary,
      unitHint: '%',
    ),
    MetricPanel(
      id: 'node_memory_utilization',
      title: 'Memory utilization',
      slug: 'nodes/memory',
      severity: PanelSeverity.warning,
      unitHint: '%',
    ),
    MetricPanel(
      id: 'node_load',
      title: '5-minute load average',
      slug: 'nodes/load',
      severity: PanelSeverity.info,
      unitHint: 'load',
    ),
    MetricPanel(
      id: 'node_network_rx',
      title: 'Network receive',
      slug: 'nodes/network-rx',
      severity: PanelSeverity.success,
      unitHint: 'MB/s',
    ),
  ],

  // ---------------- Deployment ----------------
  'deployments': [
    MetricPanel(
      id: 'deployment_replicas',
      title: 'Current replicas',
      slug: 'deployments/replicas',
      severity: PanelSeverity.success,
      unitHint: 'replicas',
    ),
    MetricPanel(
      id: 'deployment_pod_cpu',
      title: 'CPU usage (sum of owned pods)',
      slug: 'deployments/cpu',
      severity: PanelSeverity.primary,
      unitHint: 'cores',
    ),
    MetricPanel(
      id: 'deployment_pod_memory',
      title: 'Memory (sum of owned pods)',
      slug: 'deployments/memory',
      severity: PanelSeverity.warning,
      unitHint: 'MB',
    ),
  ],

  // ---------------- StatefulSet ----------------
  'statefulsets': [
    MetricPanel(
      id: 'statefulset_replicas_ready',
      title: 'Ready replicas',
      slug: 'statefulsets/replicas-ready',
      severity: PanelSeverity.success,
      unitHint: 'replicas',
    ),
    MetricPanel(
      id: 'statefulset_pod_cpu',
      title: 'CPU usage (sum of owned pods)',
      slug: 'statefulsets/cpu',
      severity: PanelSeverity.primary,
      unitHint: 'cores',
    ),
    MetricPanel(
      id: 'statefulset_pod_memory',
      title: 'Memory (sum of owned pods)',
      slug: 'statefulsets/memory',
      severity: PanelSeverity.warning,
      unitHint: 'MB',
    ),
  ],

  // ---------------- DaemonSet ----------------
  'daemonsets': [
    MetricPanel(
      id: 'daemonset_ready',
      title: 'Ready pods',
      slug: 'daemonsets/ready',
      severity: PanelSeverity.success,
      unitHint: 'pods',
    ),
    MetricPanel(
      id: 'daemonset_pod_cpu',
      title: 'CPU usage (sum of owned pods)',
      slug: 'daemonsets/cpu',
      severity: PanelSeverity.primary,
      unitHint: 'cores',
    ),
    MetricPanel(
      id: 'daemonset_pod_memory',
      title: 'Memory (sum of owned pods)',
      slug: 'daemonsets/memory',
      severity: PanelSeverity.warning,
      unitHint: 'MB',
    ),
  ],

  // ---------------- PersistentVolumeClaim ----------------
  // Backend Registry uses `pvcs/*` for the slug namespace even though
  // the K8s plural is `persistentvolumeclaims`. The mobile kind key
  // (and route segment) keep the K8s plural; the slug just references
  // the Registry entry directly.
  'persistentvolumeclaims': [
    MetricPanel(
      id: 'pvc_usage',
      title: 'Used capacity',
      slug: 'pvcs/usage',
      severity: PanelSeverity.warning,
      unitHint: 'GiB',
    ),
    MetricPanel(
      id: 'pvc_capacity',
      title: 'Total capacity',
      slug: 'pvcs/capacity',
      severity: PanelSeverity.primary,
      unitHint: 'GiB',
    ),
    MetricPanel(
      id: 'pvc_inodes',
      title: 'Inodes used',
      slug: 'pvcs/inodes',
      severity: PanelSeverity.success,
      unitHint: 'inodes',
    ),
  ],
};

/// Convenience for callsites that need an unambiguous "are there panels
/// for this kind" check before rendering the Metrics tab affordance.
bool metricsAvailableForKind(String kind) {
  final panels = metricPanelsByKind[kind];
  return panels != null && panels.isNotEmpty;
}
