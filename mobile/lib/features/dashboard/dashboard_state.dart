// Mirrors `backend/internal/k8s/resources/dashboard.go::DashboardSummary`.
// CPU/Memory utilization may be null when Prometheus is unreachable.

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
    final services = json['services'] as Map<String, dynamic>?;
    return DashboardSummary(
      nodes: NodeSummary.fromJson(json['nodes'] as Map<String, dynamic>? ?? {}),
      pods: PodSummary.fromJson(json['pods'] as Map<String, dynamic>? ?? {}),
      servicesTotal: services?['total'] as int? ?? 0,
      alerts:
          AlertSummary.fromJson(json['alerts'] as Map<String, dynamic>? ?? {}),
      cpu: json['cpu'] != null
          ? Utilization.fromJson(json['cpu'] as Map<String, dynamic>)
          : null,
      memory: json['memory'] != null
          ? Utilization.fromJson(json['memory'] as Map<String, dynamic>)
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
        total: json['total'] as int? ?? 0,
        ready: json['ready'] as int? ?? 0,
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
        total: json['total'] as int? ?? 0,
        running: json['running'] as int? ?? 0,
        pending: json['pending'] as int? ?? 0,
        failed: json['failed'] as int? ?? 0,
      );

  final int total;
  final int running;
  final int pending;
  final int failed;
}

class AlertSummary {
  const AlertSummary({required this.active, required this.critical});

  factory AlertSummary.fromJson(Map<String, dynamic> json) => AlertSummary(
        active: json['active'] as int? ?? 0,
        critical: json['critical'] as int? ?? 0,
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
        percentage: (json['percentage'] as num?)?.toDouble() ?? 0,
        used: json['used'] as String? ?? '',
        total: json['total'] as String? ?? '',
        requests: json['requests'] as String? ?? '',
        limits: json['limits'] as String? ?? '',
      );

  final double percentage;
  final String used;
  final String total;
  final String requests;
  final String limits;
}
