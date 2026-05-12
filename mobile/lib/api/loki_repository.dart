// Mobile-side wrapper over the backend Loki log API
// (`/v1/logs/{status,query,labels,labels/{name}/values,volume}`).
//
// Shape notes worth pinning before reading:
//
//   * `query` returns Loki's raw envelope under `data` —
//     `{resultType, result: Stream[]}`. Each `Stream` carries a
//     `stream` label map and a `values` array of `[ns_ts_string, line]`
//     pairs. The mobile parse lifts this into typed [LogLine] records
//     so the results list doesn't carry parsing into every render.
//
//   * `volume` returns the same envelope shape as Prometheus matrix
//     queries — `{resultType, result: VolumeEntry[]}` — where each
//     entry carries a `metric` map and `values: [[ts_seconds, count_str]]`.
//     The volume histogram aggregates across entries (single bar
//     series for the whole histogram regardless of how many metrics
//     match the query) so the parse coalesces buckets by timestamp.
//
//   * Status endpoint returns `{detected, url, detectedVia}`. As with
//     the monitoring status, a 5xx from this probe is treated as
//     "not detected" rather than an error — the FeatureUnavailableState
//     route is more actionable than a noisy error card on a flaky
//     reverse proxy.
//
//   * Namespace param threading: the backend hard-403s non-admin
//     callers that omit `namespace=` on query / labels / volume.
//     Mobile pre-flights the namespace selection so the UX surfaces
//     a "Namespace required" prompt instead of letting the operator
//     submit a query that lands in the network error path.
//
//   * 4096-character LogQL limit gated client-side before POST.
//     Matches the backend's `loki.maxQueryLen` constant — the same
//     limit also lives there so a runaway query can't burn round-
//     trip latency just to be rejected at the server.

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';

/// Maximum LogQL query length accepted by the backend.
///
/// Mirrors `backend/internal/loki/security.go::maxQueryLen`. Mobile
/// gates this client-side so an operator pasting a multi-megabyte
/// query into the LogQL textarea sees a "Query exceeds 4096 chars"
/// inline error before the request leaves the device. The backend
/// still enforces the limit; the client check is a UX shortcut, not
/// a security boundary.
const int kLokiMaxQueryChars = 4096;

/// Snapshot of the backend's `/v1/logs/status` response. `detected`
/// gates `FeatureUnavailableState.loki()`; `url` and `detectedVia`
/// surface in the editor footer for operators who need to know
/// whether Loki was auto-discovered or configured via env var.
class LokiStatus {
  const LokiStatus({
    required this.detected,
    this.url,
    this.detectedVia,
  });

  final bool detected;
  final String? url;
  final String? detectedVia;

  factory LokiStatus.fromJson(Map<String, dynamic> json) {
    return LokiStatus(
      detected: json['detected'] == true,
      url: json['url'] as String?,
      detectedVia: json['detectedVia'] as String?,
    );
  }

  static const empty = LokiStatus(detected: false);
}

/// Single log entry: nanosecond timestamp + raw line + the stream's
/// label map. Labels are shared across all lines in a `Stream` block
/// — the mobile parse flattens them onto every LogLine so per-line
/// rendering doesn't have to thread the stream context around.
class LogLine {
  const LogLine({
    required this.timestampNanos,
    required this.line,
    required this.labels,
  });

  /// Nanosecond timestamp as Loki returns it (string in wire format,
  /// parsed to int here so the results list can sort + format without
  /// re-parsing per render).
  final int timestampNanos;

  /// Raw log line as Loki captured it. May be a JSON object, a plain
  /// text message, or a stack trace fragment. Mobile renders verbatim
  /// in monospace; severity detection is best-effort over the prefix.
  final String line;

  /// Stream labels (e.g. `{namespace, pod, container, app}`). Empty
  /// when the backend's label set is dropped by the query pipeline.
  final Map<String, String> labels;

  DateTime get timestamp =>
      DateTime.fromMicrosecondsSinceEpoch(timestampNanos ~/ 1000, isUtc: true);
}

/// Typed parse of `query`'s envelope. `lines` is the flattened
/// stream/values join; `streamCount` carries the Loki-side aggregation
/// signal (operators occasionally care that "this query matched 47
/// streams" even if only 12 lines land in the cap).
class LogQueryResult {
  const LogQueryResult({
    required this.lines,
    required this.streamCount,
  });

  final List<LogLine> lines;
  final int streamCount;

  /// True when the response hit the backend's 5000-line cap.
  /// `LogResultsList` surfaces a banner so the operator knows to
  /// narrow the query rather than scrolling endlessly.
  bool get truncated => lines.length >= 5000;

  bool get isEmpty => lines.isEmpty;

  factory LogQueryResult.fromJson(Map<String, dynamic> json) {
    final result = json['result'];
    final lines = <LogLine>[];
    int streamCount = 0;
    if (result is List) {
      for (final entry in result) {
        if (entry is! Map) continue;
        streamCount++;
        final streamLabels = <String, String>{};
        final stream = entry['stream'];
        if (stream is Map) {
          for (final mEntry in stream.entries) {
            final k = mEntry.key;
            final v = mEntry.value;
            if (k is String && v is String) {
              streamLabels[k] = v;
            }
          }
        }
        final values = entry['values'];
        if (values is List) {
          for (final pair in values) {
            if (pair is! List || pair.length < 2) continue;
            final tsRaw = pair[0];
            final lineRaw = pair[1];
            final tsStr = tsRaw is String ? tsRaw : '$tsRaw';
            final tsNanos = int.tryParse(tsStr) ?? 0;
            final line = lineRaw is String ? lineRaw : '$lineRaw';
            lines.add(LogLine(
              timestampNanos: tsNanos,
              line: line,
              labels: streamLabels,
            ));
          }
        }
      }
    }
    return LogQueryResult(lines: lines, streamCount: streamCount);
  }
}

/// One bucket on the log volume histogram. The mobile parse
/// aggregates across entries so a multi-metric volume response
/// collapses to a single bar series.
class LogVolumeBucket {
  const LogVolumeBucket({required this.timestamp, required this.count});

  final DateTime timestamp;
  final int count;
}

/// Typed parse of `volume`'s envelope. Buckets are sorted by
/// timestamp; histogram render reads them in order.
class LogVolumeResult {
  const LogVolumeResult({required this.buckets});

  final List<LogVolumeBucket> buckets;

  bool get isEmpty => buckets.isEmpty;

  int get total => buckets.fold(0, (sum, b) => sum + b.count);

  factory LogVolumeResult.fromJson(Map<String, dynamic> json) {
    final result = json['result'];
    // Aggregate counts across entries at each timestamp bucket. The
    // backend may emit multiple `metric` entries when target labels
    // are set; a single bar series with summed counts is the
    // mobile-friendly view.
    final byTs = <int, int>{};
    if (result is List) {
      for (final entry in result) {
        if (entry is! Map) continue;
        final values = entry['values'];
        if (values is! List) continue;
        for (final pair in values) {
          if (pair is! List || pair.length < 2) continue;
          final tsRaw = pair[0];
          final countRaw = pair[1];
          // Loki's volume response carries timestamps as numeric
          // seconds (float), values as stringified counts. Parse
          // defensively.
          final tsSecs = tsRaw is num
              ? tsRaw.toDouble()
              : double.tryParse('$tsRaw');
          final countStr = countRaw is String ? countRaw : '$countRaw';
          final count = int.tryParse(countStr) ??
              double.tryParse(countStr)?.toInt() ??
              0;
          if (tsSecs == null) continue;
          final tsKey = (tsSecs * 1000).round();
          byTs[tsKey] = (byTs[tsKey] ?? 0) + count;
        }
      }
    }
    final buckets = byTs.entries
        .map((e) => LogVolumeBucket(
              timestamp:
                  DateTime.fromMillisecondsSinceEpoch(e.key, isUtc: true),
              count: e.value,
            ))
        .toList()
      ..sort((a, b) => a.timestamp.compareTo(b.timestamp));
    return LogVolumeResult(buckets: buckets);
  }
}

/// Mobile wrapper over the backend Loki API surface. Stateless — the
/// active cluster threads in via the Dio interceptor stack unless the
/// caller explicitly overrides via `clusterIdOverride`.
class LokiRepository {
  LokiRepository(this._dio);

  final Dio _dio;

  /// Unwraps the backend's `{data: {data: ...}}` envelope. The outer
  /// `data` is k8sCenter's response wrapper; the inner `data` is the
  /// Loki proxy's own envelope. Some shapes flatten onto the outer
  /// map, so the fallback treats the outer map as the result block —
  /// keeps the parse resilient to a backend response-shape refactor.
  Map<String, dynamic>? _unwrapLokiEnvelope(Object? raw) {
    if (raw is! Map) return null;
    final asMap = Map<String, dynamic>.from(raw);
    final nested = asMap['data'];
    if (nested is Map) {
      return Map<String, dynamic>.from(nested);
    }
    return asMap;
  }

  /// Fetches the Loki discovery status. Returns [LokiStatus.empty] on
  /// 5xx transport errors so callers can route straight to
  /// `FeatureUnavailableState.loki()` without throwing.
  Future<LokiStatus> status({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/logs/status',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return LokiStatus.fromJson(Map<String, dynamic>.from(data));
      }
      return LokiStatus.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final code = e.response?.statusCode ?? 0;
      if (code >= 500 && code < 600) {
        return LokiStatus.empty;
      }
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Runs a LogQL range query against the backend. `namespace` is
  /// passed through as a query param; the backend rewrites the query
  /// to enforce namespace RBAC for non-admin callers.
  ///
  /// Throws [ArgumentError] when [query] exceeds [kLokiMaxQueryChars].
  /// The backend enforces the same limit; the client check is a UX
  /// shortcut for the 4096-char paste case so the operator sees an
  /// inline error before waiting on a round-trip.
  Future<LogQueryResult> query({
    required String query,
    required DateTime start,
    required DateTime end,
    String? namespace,
    int limit = 1000,
    String direction = 'backward',
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    if (query.length > kLokiMaxQueryChars) {
      throw ArgumentError(
        'LogQL query exceeds $kLokiMaxQueryChars characters '
        '(${query.length}). Shorten the matcher set or use the search '
        'mode to construct a more selective query.',
      );
    }
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/logs/query',
        queryParameters: {
          'query': query,
          'start': start.toUtc().toIso8601String(),
          'end': end.toUtc().toIso8601String(),
          'limit': '$limit',
          'direction': direction,
          if (namespace != null && namespace.isNotEmpty) 'namespace': namespace,
        },
        options: Options(
          receiveTimeout: const Duration(seconds: 60),
          headers: clusterIdOverride == null
              ? null
              : {'X-Cluster-ID': clusterIdOverride},
        ),
        cancelToken: cancelToken,
      );
      final inner = _unwrapLokiEnvelope(res.data?['data']);
      if (inner == null) {
        return const LogQueryResult(lines: [], streamCount: 0);
      }
      return LogQueryResult.fromJson(inner);
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Fetches volume buckets for the histogram. Optional best-effort
  /// surface — callers swallow 503s and hide the histogram panel
  /// rather than failing the whole search.
  Future<LogVolumeResult> volume({
    required String query,
    required DateTime start,
    required DateTime end,
    required String step,
    String? namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    if (query.length > kLokiMaxQueryChars) {
      throw ArgumentError(
        'LogQL query exceeds $kLokiMaxQueryChars characters; '
        'volume request not sent.',
      );
    }
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/logs/volume',
        queryParameters: {
          'query': query,
          'start': start.toUtc().toIso8601String(),
          'end': end.toUtc().toIso8601String(),
          'step': step,
          if (namespace != null && namespace.isNotEmpty) 'namespace': namespace,
        },
        options: Options(
          receiveTimeout: const Duration(seconds: 30),
          headers: clusterIdOverride == null
              ? null
              : {'X-Cluster-ID': clusterIdOverride},
        ),
        cancelToken: cancelToken,
      );
      final inner = _unwrapLokiEnvelope(res.data?['data']);
      if (inner == null) {
        return const LogVolumeResult(buckets: []);
      }
      return LogVolumeResult.fromJson(inner);
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Lists values for a label (e.g. `pod` values within a namespace).
  /// `scopeQuery` is the LogQL selector to scope the lookup — when
  /// non-empty the backend forwards it as a `query=` param so Loki
  /// returns only labels seen within matching streams.
  Future<List<String>> labelValues({
    required String name,
    DateTime? start,
    DateTime? end,
    String? namespace,
    String? scopeQuery,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    final params = <String, String>{};
    if (start != null) params['start'] = start.toUtc().toIso8601String();
    if (end != null) params['end'] = end.toUtc().toIso8601String();
    if (namespace != null && namespace.isNotEmpty) {
      params['namespace'] = namespace;
    }
    if (scopeQuery != null && scopeQuery.isNotEmpty) {
      params['query'] = scopeQuery;
    }
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/logs/labels/${Uri.encodeComponent(name)}/values',
        queryParameters: params.isEmpty ? null : params,
        // 15s receiveTimeout caps how long the cascade dropdown sits
        // on "Loading…" if the Loki label endpoint goes silent
        // (backend pod restart, TCP half-open). Backend's own
        // LabelValues context timeout is 10s (loki/client.go) so a
        // well-behaved backend always responds first; this is the
        // fallback for crashed/half-open paths.
        options: Options(
          receiveTimeout: const Duration(seconds: 15),
          headers: clusterIdOverride == null
              ? null
              : {'X-Cluster-ID': clusterIdOverride},
        ),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is List) {
        return data.whereType<String>().toList();
      }
      return const <String>[];
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      // Label lookups are best-effort UX — a 403 (non-admin without
      // namespace) or 502 (Loki transient) shouldn't crash the
      // dropdown. Return empty so the picker degrades gracefully and
      // the operator can still type a value manually.
      final code = e.response?.statusCode ?? 0;
      if (code == 403 || code == 502 || code == 503) {
        return const <String>[];
      }
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Lists all label names. Returns empty list on 5xx so the editor
  /// label browser degrades gracefully when Loki is briefly
  /// unavailable mid-session.
  Future<List<String>> labels({
    DateTime? start,
    DateTime? end,
    String? namespace,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    final params = <String, String>{};
    if (start != null) params['start'] = start.toUtc().toIso8601String();
    if (end != null) params['end'] = end.toUtc().toIso8601String();
    if (namespace != null && namespace.isNotEmpty) {
      params['namespace'] = namespace;
    }
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/logs/labels',
        queryParameters: params.isEmpty ? null : params,
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is List) {
        return data.whereType<String>().toList();
      }
      return const <String>[];
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final code = e.response?.statusCode ?? 0;
      if (code == 403 || code >= 500) {
        return const <String>[];
      }
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final lokiRepositoryProvider = Provider<LokiRepository>((ref) {
  return LokiRepository(ref.watch(dioProvider));
});

/// Per-cluster Loki status. Keyed on the cluster id so cluster switches
/// key a fresh entry rather than reusing the prior cluster's gate
/// decision (a false-negative would route the editor into the
/// FeatureUnavailableState until manual refresh).
///
/// Cancellation handling: when the autoDispose ref is invalidated
/// mid-fetch (cluster switch, screen pop), the CancelToken fires and
/// the await throws `DioException(type: cancel)`. We swallow that case
/// to return `LokiStatus.empty` rather than rethrow — the next-built
/// provider entry will produce the fresh status, and surfacing a
/// transient AsyncError briefly flashes an unhelpful error card.
final lokiStatusProvider =
    FutureProvider.autoDispose.family<LokiStatus, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('status invalidated');
  });
  try {
    return await ref.read(lokiRepositoryProvider).status(
          clusterIdOverride: clusterId,
          cancelToken: cancel,
        );
  } on DioException catch (e) {
    if (CancelToken.isCancel(e)) return LokiStatus.empty;
    rethrow;
  }
});
