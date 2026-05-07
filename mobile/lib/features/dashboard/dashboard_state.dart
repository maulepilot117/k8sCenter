// Mirrors `backend/internal/k8s/resources/dashboard.go::DashboardSummary`.
// CPU/Memory utilization may be null when Prometheus is unreachable. The
// backend also sometimes returns a synthetic `Utilization{Percentage:0,
// Used:'N/A'}` when Prometheus errors but allocatable>0 — `unavailable`
// detects that case so the UI can render an "Unavailable" placeholder
// instead of a confident-looking 0%.
//
// JSON parsing uses `(json[k] as num?)?.toX()` instead of `as int?` /
// `as double?` because Dart does not coerce JSON doubles to int. A
// backend value of `5` (int) and `5.0` (double) both deserialize as
// `num`, and only the `num` cast is permissive enough to handle both.

class DashboardSummary {
  const DashboardSummary({
    required this.nodes,
    required this.pods,
    required this.servicesTotal,
    required this.alerts,
    this.cpu,
    this.memory,
  });

  factory DashboardSummary.fromJson(Map<String, dynamic> json) {
    final services = _asMap(json['services']);
    return DashboardSummary(
      nodes: NodeSummary.fromJson(_asMap(json['nodes'])),
      pods: PodSummary.fromJson(_asMap(json['pods'])),
      servicesTotal: _asInt(services['total']),
      alerts: AlertSummary.fromJson(_asMap(json['alerts'])),
      cpu: json['cpu'] is Map
          ? Utilization.fromJson(_asMap(json['cpu']))
          : null,
      memory: json['memory'] is Map
          ? Utilization.fromJson(_asMap(json['memory']))
          : null,
    );
  }

  final NodeSummary nodes;
  final PodSummary pods;
  final int servicesTotal;
  final AlertSummary alerts;
  final Utilization? cpu;
  final Utilization? memory;
}

class NodeSummary {
  const NodeSummary({required this.total, required this.ready});

  factory NodeSummary.fromJson(Map<String, dynamic> json) => NodeSummary(
        total: _asInt(json['total']),
        ready: _asInt(json['ready']),
      );

  final int total;
  final int ready;
}

class PodSummary {
  const PodSummary({
    required this.total,
    required this.running,
    required this.pending,
    required this.failed,
  });

  factory PodSummary.fromJson(Map<String, dynamic> json) => PodSummary(
        total: _asInt(json['total']),
        running: _asInt(json['running']),
        pending: _asInt(json['pending']),
        failed: _asInt(json['failed']),
      );

  final int total;
  final int running;
  final int pending;
  final int failed;
}

class AlertSummary {
  const AlertSummary({required this.active, required this.critical});

  factory AlertSummary.fromJson(Map<String, dynamic> json) => AlertSummary(
        active: _asInt(json['active']),
        critical: _asInt(json['critical']),
      );

  final int active;
  final int critical;
}

class Utilization {
  const Utilization({
    required this.percentage,
    required this.used,
    required this.total,
    required this.requests,
    required this.limits,
  });

  factory Utilization.fromJson(Map<String, dynamic> json) => Utilization(
        percentage: _asDouble(json['percentage']),
        used: _asString(json['used']),
        total: _asString(json['total']),
        requests: _asString(json['requests']),
        limits: _asString(json['limits']),
      );

  final double percentage;
  final String used;
  final String total;
  final String requests;
  final String limits;

  /// True when the backend signals "Prometheus unreachable" by emitting
  /// the synthetic `Utilization{Percentage:0, Used:'N/A'}` payload — see
  /// `backend/internal/k8s/resources/dashboard.go`. UI renders an
  /// "Unavailable" placeholder rather than a misleading 0% gauge.
  bool get unavailable =>
      used.trim().toUpperCase() == 'N/A' && percentage == 0;

  /// True when the backend supplied a finite percentage we can render.
  bool get hasFinitePercentage => percentage.isFinite;
}

// --- Defensive coercion helpers ---
//
// Backend JSON sometimes drifts (Prometheus emits doubles, fields go
// missing during partial outages). These helpers absorb the common
// drift cases so a single bad number doesn't crash the dashboard.

int _asInt(Object? v) {
  if (v is num && v.isFinite) return v.toInt();
  return 0;
}

double _asDouble(Object? v) {
  if (v is num && v.isFinite) return v.toDouble();
  return 0;
}

String _asString(Object? v) {
  if (v is String) return v;
  if (v == null) return '';
  return v.toString();
}

Map<String, dynamic> _asMap(Object? v) {
  if (v is Map<String, dynamic>) return v;
  if (v is Map) return Map<String, dynamic>.from(v);
  return <String, dynamic>{};
}
