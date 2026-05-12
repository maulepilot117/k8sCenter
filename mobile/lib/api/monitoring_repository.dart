// Mobile-side wrapper over the backend monitoring API
// (`/v1/monitoring/{status,query,query_range,templates,templates/query,
// resource-dashboard}`).
//
// Shape notes worth pinning before reading:
//
//   * `query_range` returns Prometheus' raw envelope under `data` —
//     `{resultType, result, warnings}`. `result` is a list of series,
//     each carrying `metric` (label map) and `values`
//     (`[ts_seconds_float, "value_string"]`). PR-4b's chart layer lifts
//     this into typed `MetricsSeries` records via [QueryRangeResult].
//
//   * `resource-dashboard` does NOT return curated PromQL — it returns
//     Grafana embed metadata (`{available, dashboardUID, varName,
//     grafanaProxied}`). The plan's "Deferred to Implementation"
//     section flagged this; the real curated PromQL lives in the
//     QueryTemplates map mirrored at
//     `lib/features/observability/metrics/metric_panels.dart`.
//
//   * Status endpoint returns `{detected, prometheus, grafana, ...}`.
//     `detected: false` is the gate for `FeatureUnavailableState
//     .monitoring()`.
//
// Cluster pinning matches `ResourceRepository`: every call accepts
// `clusterIdOverride` and forwards it as an explicit `X-Cluster-ID`
// header so the cache slot keyed on cluster id and the wire request
// always agree, defending against the M3 PR-3c pinned-cluster bug.

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';

/// Snapshot of the backend's `/v1/monitoring/status` response.
/// `detected` drives the `FeatureUnavailableState` gate; the per-engine
/// flags surface in the metrics tab footer for operators who need to
/// know whether Grafana embedding is also available.
class MonitoringStatus {
  const MonitoringStatus({
    required this.detected,
    required this.prometheusAvailable,
    required this.grafanaAvailable,
    this.message,
  });

  final bool detected;
  final bool prometheusAvailable;
  final bool grafanaAvailable;
  final String? message;

  factory MonitoringStatus.fromJson(Map<String, dynamic> json) {
    final prom = json['prometheus'];
    final graf = json['grafana'];
    final promAvail = prom is Map && prom['available'] == true;
    final grafAvail = graf is Map && graf['available'] == true;
    final detectedField = json['detected'];
    // Some discoverer states omit `detected`; fall back to "either side
    // available" as the activation signal.
    final detected = detectedField is bool
        ? detectedField
        : (promAvail || grafAvail);
    return MonitoringStatus(
      detected: detected,
      prometheusAvailable: promAvail,
      grafanaAvailable: grafAvail,
      message: json['message'] as String?,
    );
  }

  static const empty = MonitoringStatus(
    detected: false,
    prometheusAvailable: false,
    grafanaAvailable: false,
  );
}

/// Single point on a series: timestamp + numeric value.
typedef MetricsPoint = ({DateTime t, double v});

/// One labelled series — `labels` is the metric's label map (e.g.
/// `{container: "web"}`), `points` is the time series.
class MetricsSeriesData {
  const MetricsSeriesData({required this.labels, required this.points});

  final Map<String, String> labels;
  final List<MetricsPoint> points;
}

/// Typed parse of the `query_range` response envelope. `warnings`
/// surfaces Prometheus admonitions inline; `resultType` is informational
/// (always `matrix` for range queries) and kept for parity with web.
class QueryRangeResult {
  const QueryRangeResult({
    required this.resultType,
    required this.series,
    required this.warnings,
  });

  final String resultType;
  final List<MetricsSeriesData> series;
  final List<String> warnings;

  /// True when no series came back (Prometheus returned `result: []`).
  /// The chart layer renders a "No data" banner rather than a flat-zero
  /// chart so operators can distinguish "metric exists with value 0"
  /// from "metric not collected for this resource".
  bool get isEmpty => series.isEmpty;

  factory QueryRangeResult.fromJson(Map<String, dynamic> json) {
    final result = json['result'];
    final warnings = (json['warnings'] as List?)
            ?.whereType<String>()
            .toList() ??
        const <String>[];
    final List<MetricsSeriesData> parsed = [];
    if (result is List) {
      for (final entry in result) {
        if (entry is! Map) continue;
        final metric = entry['metric'];
        final values = entry['values'];
        final labels = <String, String>{};
        if (metric is Map) {
          for (final mEntry in metric.entries) {
            final key = mEntry.key;
            final val = mEntry.value;
            if (key is String && val is String) {
              labels[key] = val;
            }
          }
        }
        final points = <MetricsPoint>[];
        if (values is List) {
          for (final pair in values) {
            if (pair is! List || pair.length < 2) continue;
            final tRaw = pair[0];
            final vRaw = pair[1];
            final tSecs = tRaw is num ? tRaw.toDouble() : double.tryParse('$tRaw');
            // Prometheus emits the value as a stringified float so very
            // large counters survive JSON precision; parse defensively.
            final v = vRaw is num ? vRaw.toDouble() : double.tryParse('$vRaw');
            if (tSecs == null || v == null) continue;
            points.add((
              t: DateTime.fromMillisecondsSinceEpoch(
                (tSecs * 1000).round(),
                isUtc: true,
              ),
              v: v,
            ));
          }
        }
        parsed.add(MetricsSeriesData(labels: labels, points: points));
      }
    }
    return QueryRangeResult(
      resultType: json['resultType'] as String? ?? 'matrix',
      series: parsed,
      warnings: warnings,
    );
  }
}

/// Mobile wrapper over the backend monitoring API surface. Stateless —
/// the active cluster threads in via the Dio interceptor stack unless
/// the caller explicitly overrides via `clusterIdOverride`.
class MonitoringRepository {
  MonitoringRepository(this._dio);

  final Dio _dio;

  /// Ranges and snap presets that mirror the plan's pinned test
  /// scenarios. See [computeStep] below.
  static const List<int> _stepPresetSeconds = [
    15,
    30,
    60,
    300,
    1800,
    3600,
  ];

  /// Computes the `step` (seconds) for a `query_range` call given the
  /// total range in seconds. Mirrors the plan's pinned values:
  ///
  ///   _computeStep(60)     == 15
  ///   _computeStep(3600)   == 15
  ///   _computeStep(21600)  == 30
  ///   _computeStep(86400)  == 300
  ///   _computeStep(604800) == 1800
  ///
  /// Strategy: take `max(ceil(rangeSec / 1000), 15)` — the per-1000-
  /// datapoint resolution target — then snap up to the nearest preset
  /// in `{15s, 30s, 1m, 5m, 30m, 1h}`. Snap-up keeps the datapoint
  /// count at or below 1000 (Prometheus' default cap is ~11000 per
  /// response, and the 1000-bucket budget keeps charts crisp without
  /// burning Prometheus query time on points the chart can't render).
  ///
  /// Note for cross-stack reviewers: the web's `PromQLQuery.tsx` uses
  /// `max(round(rangeMs / 200_000), 15)` (~200 datapoints, no preset
  /// snapping). The plan asserts these match, but they don't — web
  /// will sit on slightly different `step=` values for the same range
  /// and the stated "shared Prometheus cache hits" benefit is partial.
  /// Mobile sticks to the plan's pinned test values; web alignment is a
  /// follow-up tracked in the plan's risk table.
  static int computeStep(int rangeSec) {
    final raw = (rangeSec / 1000).ceil();
    final clamped = raw < 15 ? 15 : raw;
    for (final preset in _stepPresetSeconds) {
      if (clamped <= preset) return preset;
    }
    return _stepPresetSeconds.last;
  }

  /// Fetches the monitoring discovery status. Returns
  /// [MonitoringStatus.empty] on transport errors so callers can route
  /// straight to `FeatureUnavailableState.monitoring()` without
  /// throwing — a network hiccup on a feature gate should surface the
  /// "not configured" path, not an error card.
  Future<MonitoringStatus> status({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/monitoring/status',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return MonitoringStatus.fromJson(Map<String, dynamic>.from(data));
      }
      return MonitoringStatus.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      // Treat any 5xx as "monitoring not reachable" — operators on a
      // flaky reverse-proxy shouldn't see a noisy error card when the
      // feature-unavailable state is more actionable. Backend
      // distinguishes "Prometheus down" (503 with message) from
      // "Prometheus errored on a specific query" (502 from
      // /query_range), and only the latter is operator-actionable;
      // the status endpoint is purely a probe.
      final code = e.response?.statusCode ?? 0;
      if (code >= 500 && code < 600) {
        return MonitoringStatus.empty;
      }
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Runs a Prometheus range query. `start` and `end` are wire-encoded
  /// as RFC3339; `step` is a Go-style duration (`15s`, `1m`, etc.) —
  /// the backend's `time.ParseDuration` is strict so we format with a
  /// single unit suffix and avoid composite forms.
  Future<QueryRangeResult> queryRange({
    required String query,
    required DateTime start,
    required DateTime end,
    required int stepSeconds,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    final step = _formatStep(stepSeconds);
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/monitoring/query_range',
        queryParameters: {
          'query': query,
          'start': start.toUtc().toIso8601String(),
          'end': end.toUtc().toIso8601String(),
          'step': step,
        },
        options: Options(
          receiveTimeout: const Duration(seconds: 30),
          headers: clusterIdOverride == null
              ? null
              : {'X-Cluster-ID': clusterIdOverride},
        ),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return QueryRangeResult.fromJson(Map<String, dynamic>.from(data));
      }
      return const QueryRangeResult(
        resultType: 'matrix',
        series: [],
        warnings: [],
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Formats a step duration for the backend's `time.ParseDuration`.
  /// Prefers the largest single unit so `3600` becomes `1h` (cleaner
  /// in logs) while sub-minute durations stay in seconds.
  String _formatStep(int seconds) {
    if (seconds <= 0) return '15s';
    if (seconds % 3600 == 0) return '${seconds ~/ 3600}h';
    if (seconds % 60 == 0) return '${seconds ~/ 60}m';
    return '${seconds}s';
  }
}

final monitoringRepositoryProvider =
    Provider<MonitoringRepository>((ref) {
  return MonitoringRepository(ref.watch(dioProvider));
});

/// Per-cluster monitoring status. Keyed on the cluster id so cluster
/// switches key a fresh entry rather than reusing the prior cluster's
/// gate decision (a false-negative would route every metrics tab into
/// the FeatureUnavailableState until manual refresh).
final monitoringStatusProvider = FutureProvider.autoDispose
    .family<MonitoringStatus, String>((ref, clusterId) async {
  // Watch active so autoDispose tear-down fires on switch even though
  // clusterId is in the family key.
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('status invalidated');
  });
  try {
    return await ref.read(monitoringRepositoryProvider).status(
          clusterIdOverride: clusterId,
          cancelToken: cancel,
        );
  } on DioException catch (e) {
    if (CancelToken.isCancel(e)) rethrow;
    rethrow;
  }
});
