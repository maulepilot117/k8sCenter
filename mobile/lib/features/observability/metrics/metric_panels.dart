// Per-kind curated PromQL panels for the M4 Metrics tab.
//
// The backend's `/v1/monitoring/resource-dashboard?kind=...` endpoint
// returns only Grafana embed metadata (`{dashboardUID, varName,
// grafanaProxied}`), NOT the curated PromQL queries the plan describes.
// The real per-kind queries live in `backend/internal/monitoring/
// metrics.go` under `QueryTemplates` — exposed via the templates
// endpoint family but limited to instant queries.
//
// PR-4b's "Deferred to Implementation" question delegated this shape
// to the implementing agent. The cleanest path within the
// "no backend changes" constraint is to mirror the backend's
// QueryTemplates queries on mobile, keyed by kind, with explicit
// variable slots that the controller fills before calling
// `/v1/monitoring/query_range`. The mobile + backend pair stays in
// sync the same way mobile-side resource models mirror backend wire
// types — drift caught at integration test time.
//
// Coverage parity with the web's Grafana dashboards:
//   * pods, nodes — rich coverage (4 panels each)
//   * persistentvolumeclaims, deployments — one canonical panel each
//   * statefulsets, daemonsets — workload-shaped panels using a
//     name-prefix matcher on `pod` labels (mirrors how
//     `kubectl top` aggregates).
//
// PromQL strings here MUST match the backend's QueryTemplates byte-for-
// byte for any template that exists there. New panels (StatefulSet,
// DaemonSet) live only here for now; if the backend adds matching
// templates, prefer those and delete the local copy.

import 'package:flutter/foundation.dart' show immutable;

/// Severity hint the chart layer uses to pick a [KubeColors] token.
/// Mirrors `KubeChartSeverity` exactly — duplicated as a domain enum so
/// the registry doesn't import the widget layer (keeps the registry
/// testable without pulling in Flutter).
enum PanelSeverity { primary, success, warning, error, info, muted }

/// A single chart panel definition. The PromQL `queryTemplate` is
/// rendered by substituting `$<name>` tokens against the panel's
/// declared `variables`. The chart layer takes the rendered string,
/// hands it to `MonitoringRepository.queryRange`, and routes the result
/// into a `KubeLineChart`.
@immutable
class MetricPanel {
  const MetricPanel({
    required this.id,
    required this.title,
    required this.queryTemplate,
    required this.variables,
    this.severity = PanelSeverity.primary,
    this.unitHint,
  });

  /// Stable id used by tests and the controller's per-panel state map.
  final String id;

  /// Operator-facing chart title (e.g. "CPU usage per container").
  final String title;

  /// PromQL with `$varname` tokens. The set of required tokens MUST
  /// equal [variables] — the controller raises an ArgumentError
  /// otherwise (caught by tests).
  final String queryTemplate;

  /// Variable names that must appear in the supplied bindings.
  final List<String> variables;

  /// Color token for the chart line(s) in this panel.
  final PanelSeverity severity;

  /// Optional unit label rendered above the chart (e.g. "cores", "%",
  /// "bytes/sec"). Not authoritative — Prometheus values arrive
  /// unitless; this is purely operator copy.
  final String? unitHint;

  /// Substitutes `$<varname>` tokens. Throws [ArgumentError] when a
  /// declared variable is missing from `vars` so the caller sees the
  /// failure synchronously rather than emitting an empty Prometheus
  /// query that returns nothing without explanation.
  String render(Map<String, String> vars) {
    var q = queryTemplate;
    for (final v in variables) {
      final value = vars[v];
      if (value == null || value.isEmpty) {
        throw ArgumentError('Missing variable for panel $id: $v');
      }
      if (!_isValidK8sName(value)) {
        throw ArgumentError(
          'Invalid value for $v on panel $id: '
          'must be a valid Kubernetes name',
        );
      }
      q = q.replaceAll('\$$v', value);
    }
    return q;
  }
}

/// Mirrors the backend's `isValidK8sName` so client-side variable
/// substitution catches injection attempts before they hit the
/// backend's parser. Lowercase alnum + `-` + `.`, 1..253 chars.
bool _isValidK8sName(String s) {
  if (s.isEmpty || s.length > 253) return false;
  for (final cu in s.codeUnits) {
    final isLower = cu >= 0x61 && cu <= 0x7A;
    final isDigit = cu >= 0x30 && cu <= 0x39;
    final isDash = cu == 0x2D;
    final isDot = cu == 0x2E;
    if (!(isLower || isDigit || isDash || isDot)) return false;
  }
  return true;
}

/// Curated panels per resource kind. Keys are the backend-canonical
/// plural kind strings (matching the route segment and
/// `ResourceDashboardMap` keys).
const Map<String, List<MetricPanel>> metricPanelsByKind = {
  // ---------------- Pod ----------------
  // Mirrors backend/QueryTemplates entries pod_cpu_usage,
  // pod_memory_usage, pod_network_rx, pod_network_tx.
  'pods': [
    MetricPanel(
      id: 'pod_cpu_usage',
      title: 'CPU usage per container',
      queryTemplate:
          'sum(rate(container_cpu_usage_seconds_total{container!="",pod="\$pod",namespace="\$namespace"}[5m])) by (container)',
      variables: ['namespace', 'pod'],
      severity: PanelSeverity.primary,
      unitHint: 'cores',
    ),
    MetricPanel(
      id: 'pod_memory_usage',
      title: 'Memory working set per container',
      queryTemplate:
          'sum(container_memory_working_set_bytes{container!="",pod="\$pod",namespace="\$namespace"}) by (container)',
      variables: ['namespace', 'pod'],
      severity: PanelSeverity.warning,
      unitHint: 'bytes',
    ),
    MetricPanel(
      id: 'pod_network_rx',
      title: 'Network receive',
      queryTemplate:
          'sum(rate(container_network_receive_bytes_total{pod="\$pod",namespace="\$namespace"}[5m]))',
      variables: ['namespace', 'pod'],
      severity: PanelSeverity.info,
      unitHint: 'bytes/sec',
    ),
    MetricPanel(
      id: 'pod_network_tx',
      title: 'Network transmit',
      queryTemplate:
          'sum(rate(container_network_transmit_bytes_total{pod="\$pod",namespace="\$namespace"}[5m]))',
      variables: ['namespace', 'pod'],
      severity: PanelSeverity.success,
      unitHint: 'bytes/sec',
    ),
  ],

  // ---------------- Node ----------------
  // Mirrors backend/QueryTemplates node_* entries.
  'nodes': [
    MetricPanel(
      id: 'node_cpu_utilization',
      title: 'CPU utilization',
      queryTemplate:
          '100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle",instance=~"\$node.*"}[5m])) * 100)',
      variables: ['node'],
      severity: PanelSeverity.primary,
      unitHint: '%',
    ),
    MetricPanel(
      id: 'node_memory_utilization',
      title: 'Memory utilization',
      queryTemplate:
          '100 * (1 - node_memory_MemAvailable_bytes{instance=~"\$node.*"} / node_memory_MemTotal_bytes{instance=~"\$node.*"})',
      variables: ['node'],
      severity: PanelSeverity.warning,
      unitHint: '%',
    ),
    MetricPanel(
      id: 'node_disk_utilization',
      title: 'Root disk utilization',
      queryTemplate:
          '100 - (node_filesystem_avail_bytes{instance=~"\$node.*",mountpoint="/",fstype!="rootfs"} / node_filesystem_size_bytes{instance=~"\$node.*",mountpoint="/",fstype!="rootfs"} * 100)',
      variables: ['node'],
      severity: PanelSeverity.error,
      unitHint: '%',
    ),
    MetricPanel(
      id: 'node_pod_count',
      title: 'Pods on node',
      queryTemplate: 'count(kube_pod_info{node="\$node"})',
      variables: ['node'],
      severity: PanelSeverity.info,
      unitHint: 'pods',
    ),
  ],

  // ---------------- Deployment ----------------
  // Mirrors backend deployment_replica_health and adds per-deployment
  // CPU/memory roll-ups that aggregate by pod-name prefix — same
  // matcher `kubectl top` uses when --by-deployment isn't supported.
  'deployments': [
    MetricPanel(
      id: 'deployment_replica_health',
      title: 'Replica health',
      queryTemplate:
          'kube_deployment_status_replicas_available{namespace="\$namespace",deployment="\$deployment"} / kube_deployment_spec_replicas{namespace="\$namespace",deployment="\$deployment"}',
      variables: ['namespace', 'deployment'],
      severity: PanelSeverity.success,
      unitHint: 'ratio',
    ),
    MetricPanel(
      id: 'deployment_pod_cpu',
      title: 'CPU usage (sum of owned pods)',
      queryTemplate:
          'sum(rate(container_cpu_usage_seconds_total{container!="",namespace="\$namespace",pod=~"\$deployment-.*"}[5m]))',
      variables: ['namespace', 'deployment'],
      severity: PanelSeverity.primary,
      unitHint: 'cores',
    ),
    MetricPanel(
      id: 'deployment_pod_memory',
      title: 'Memory (sum of owned pods)',
      queryTemplate:
          'sum(container_memory_working_set_bytes{container!="",namespace="\$namespace",pod=~"\$deployment-.*"})',
      variables: ['namespace', 'deployment'],
      severity: PanelSeverity.warning,
      unitHint: 'bytes',
    ),
  ],

  // ---------------- StatefulSet ----------------
  // No backend QueryTemplates coverage today; the matcher relies on
  // StatefulSet's predictable `name-<ordinal>` pod naming convention.
  'statefulsets': [
    MetricPanel(
      id: 'statefulset_replica_health',
      title: 'Ready replicas',
      queryTemplate:
          'kube_statefulset_status_replicas_ready{namespace="\$namespace",statefulset="\$statefulset"} / kube_statefulset_status_replicas{namespace="\$namespace",statefulset="\$statefulset"}',
      variables: ['namespace', 'statefulset'],
      severity: PanelSeverity.success,
      unitHint: 'ratio',
    ),
    MetricPanel(
      id: 'statefulset_pod_cpu',
      title: 'CPU usage (sum of owned pods)',
      queryTemplate:
          'sum(rate(container_cpu_usage_seconds_total{container!="",namespace="\$namespace",pod=~"\$statefulset-[0-9]+"}[5m]))',
      variables: ['namespace', 'statefulset'],
      severity: PanelSeverity.primary,
      unitHint: 'cores',
    ),
    MetricPanel(
      id: 'statefulset_pod_memory',
      title: 'Memory (sum of owned pods)',
      queryTemplate:
          'sum(container_memory_working_set_bytes{container!="",namespace="\$namespace",pod=~"\$statefulset-[0-9]+"})',
      variables: ['namespace', 'statefulset'],
      severity: PanelSeverity.warning,
      unitHint: 'bytes',
    ),
  ],

  // ---------------- DaemonSet ----------------
  // Backend has no curated DS templates. The desired/available ratio
  // catches partial node coverage; per-pod CPU/memory uses the same
  // prefix matcher as Deployment.
  'daemonsets': [
    MetricPanel(
      id: 'daemonset_ready_ratio',
      title: 'Ready ratio',
      queryTemplate:
          'kube_daemonset_status_number_ready{namespace="\$namespace",daemonset="\$daemonset"} / kube_daemonset_status_desired_number_scheduled{namespace="\$namespace",daemonset="\$daemonset"}',
      variables: ['namespace', 'daemonset'],
      severity: PanelSeverity.success,
      unitHint: 'ratio',
    ),
    MetricPanel(
      id: 'daemonset_pod_cpu',
      title: 'CPU usage (sum of owned pods)',
      queryTemplate:
          'sum(rate(container_cpu_usage_seconds_total{container!="",namespace="\$namespace",pod=~"\$daemonset-.*"}[5m]))',
      variables: ['namespace', 'daemonset'],
      severity: PanelSeverity.primary,
      unitHint: 'cores',
    ),
    MetricPanel(
      id: 'daemonset_pod_memory',
      title: 'Memory (sum of owned pods)',
      queryTemplate:
          'sum(container_memory_working_set_bytes{container!="",namespace="\$namespace",pod=~"\$daemonset-.*"})',
      variables: ['namespace', 'daemonset'],
      severity: PanelSeverity.warning,
      unitHint: 'bytes',
    ),
  ],

  // ---------------- PersistentVolumeClaim ----------------
  // Mirrors backend pvc_usage_percent; capacity/available panels round
  // out the trio operators want when investigating a "PVC almost full"
  // alert.
  'persistentvolumeclaims': [
    MetricPanel(
      id: 'pvc_usage_percent',
      title: 'Used capacity',
      queryTemplate:
          'kubelet_volume_stats_used_bytes{namespace="\$namespace",persistentvolumeclaim="\$pvc"} / kubelet_volume_stats_capacity_bytes{namespace="\$namespace",persistentvolumeclaim="\$pvc"} * 100',
      variables: ['namespace', 'pvc'],
      severity: PanelSeverity.warning,
      unitHint: '%',
    ),
    MetricPanel(
      id: 'pvc_used_bytes',
      title: 'Used bytes',
      queryTemplate:
          'kubelet_volume_stats_used_bytes{namespace="\$namespace",persistentvolumeclaim="\$pvc"}',
      variables: ['namespace', 'pvc'],
      severity: PanelSeverity.primary,
      unitHint: 'bytes',
    ),
    MetricPanel(
      id: 'pvc_available_bytes',
      title: 'Available bytes',
      queryTemplate:
          'kubelet_volume_stats_available_bytes{namespace="\$namespace",persistentvolumeclaim="\$pvc"}',
      variables: ['namespace', 'pvc'],
      severity: PanelSeverity.success,
      unitHint: 'bytes',
    ),
  ],
};

/// Convenience for callsites that need an unambiguous "are there panels
/// for this kind" check before rendering the Metrics tab affordance.
bool metricsAvailableForKind(String kind) {
  final panels = metricPanelsByKind[kind];
  return panels != null && panels.isNotEmpty;
}
