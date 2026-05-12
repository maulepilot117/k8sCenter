// Controller behind the LogQL editor screen. Owns the submitted query
// + range + result + volume buckets; the LogFilterBar manages its own
// form state and emits a parameter bundle on Run.
//
// Race protection comes from the `RefreshableController` mixin —
//   * supersede() rotates the CancelToken + bumps the dispatch id on
//     every submit so a fast-tapping operator's prior in-flight fetch
//     can't write over a fresh result.
//   * Cluster pin is re-checked at result arrival (postEmission). The
//     wire request itself was already pinned via `X-Cluster-ID`, so a
//     post-emission mismatch surfaces the "request landed on pinned
//     cluster" copy rather than silently writing a different cluster's
//     log lines under the active cluster's screen.
//   * isDisposed gates every post-await state write.
//
// Query + volume run in parallel (Future.wait). Volume is best-effort —
// a 5xx surfaces as a hidden volume panel rather than a whole-screen
// error, mirroring the web's "non-critical" handling in LogExplorer.

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/api_error.dart';
import '../../../api/loki_repository.dart';
import '../../../cluster/cluster_provider.dart';
import '../../../widgets/refreshable_controller.dart';
import '../../../widgets/time_range_picker.dart';

/// Editor mode. `search` builds the LogQL string from cascading
/// dropdowns + free-text contains; `logql` passes the operator's raw
/// text through verbatim.
enum LogQueryMode { search, logql }

/// Snapshot of the submitted query's primary result.
sealed class LogQueryStatus {
  const LogQueryStatus();
}

/// Initial state before any query has run. The results panel renders
/// a "Run a query to see results" prompt; the volume histogram stays
/// hidden.
class LogQueryIdle extends LogQueryStatus {
  const LogQueryIdle();
}

class LogQueryLoading extends LogQueryStatus {
  const LogQueryLoading();
}

class LogQueryLoaded extends LogQueryStatus {
  const LogQueryLoaded(this.result);
  final LogQueryResult result;
}

class LogQueryFailed extends LogQueryStatus {
  const LogQueryFailed(this.message);
  final String message;
}

/// Snapshot of the volume histogram fetch. Hidden when [LogVolumeHidden]
/// — either because no query has run yet, the volume fetch returned an
/// error (best-effort surface), or the response was empty.
sealed class LogVolumeStatus {
  const LogVolumeStatus();
}

class LogVolumeHidden extends LogVolumeStatus {
  const LogVolumeHidden();
}

class LogVolumeLoading extends LogVolumeStatus {
  const LogVolumeLoading();
}

class LogVolumeLoaded extends LogVolumeStatus {
  const LogVolumeLoaded(this.result);
  final LogVolumeResult result;
}

/// Bundled query parameters emitted by the filter bar on Run. Mirrors
/// the backend's `/v1/logs/query` query-param surface — the controller
/// passes these straight through.
class LogSearchParams {
  const LogSearchParams({
    required this.namespace,
    required this.query,
    required this.range,
    this.limit = 1000,
    this.direction = 'backward',
  });

  final String? namespace;
  final String query;
  final TimeRange range;
  final int limit;
  final String direction;
}

/// State the screen consumes. Two AsyncValue-shaped fields (result +
/// volume) because the two surfaces render independently — a volume
/// 5xx mustn't blank the results panel.
class LogSearchState {
  const LogSearchState({
    required this.params,
    required this.result,
    required this.volume,
  });

  /// Last submitted params. `null` before the first Run press, used
  /// only for `refresh()`'s replay path.
  final LogSearchParams? params;

  final LogQueryStatus result;
  final LogVolumeStatus volume;

  LogSearchState copyWith({
    LogSearchParams? params,
    LogQueryStatus? result,
    LogVolumeStatus? volume,
  }) {
    return LogSearchState(
      params: params ?? this.params,
      result: result ?? this.result,
      volume: volume ?? this.volume,
    );
  }

  static const initial = LogSearchState(
    params: null,
    result: LogQueryIdle(),
    volume: LogVolumeHidden(),
  );
}

/// Volume step allowlist. The backend's `loki.allowedSteps` rejects
/// anything outside this set; the mobile picker walks the list to
/// find the largest step that keeps the bucket count under the cap.
const List<({String label, int seconds})> _kVolumeSteps = [
  (label: '15s', seconds: 15),
  (label: '30s', seconds: 30),
  (label: '1m', seconds: 60),
  (label: '5m', seconds: 300),
  (label: '15m', seconds: 900),
  (label: '30m', seconds: 1800),
  (label: '1h', seconds: 3600),
  (label: '6h', seconds: 21600),
  (label: '1d', seconds: 86400),
];

/// Picks the **smallest** volume step that keeps the bucket count
/// ≤ [maxBuckets] for the selected range — the densest histogram the
/// cap allows. Walks [_kVolumeSteps] from smallest (15s) to largest
/// (1d) and returns the first entry that fits.
///
/// When the range exceeds what 1d can fit under the cap (multi-month
/// custom range), falls back to 1d rather than throwing — fl_chart
/// and the phone GPU can handle a few hundred bars, and the bar count
/// stays bounded.
String chooseVolumeStep(int rangeSec, {int maxBuckets = 120}) {
  for (final step in _kVolumeSteps) {
    if ((rangeSec / step.seconds).ceil() <= maxBuckets) {
      return step.label;
    }
  }
  return _kVolumeSteps.last.label;
}

class LogSearchController
    extends AutoDisposeFamilyNotifier<LogSearchState, String>
    with RefreshableController {
  late String _clusterId;

  @override
  String get pinnedClusterId => _clusterId;

  @override
  String currentActiveClusterId(Ref ref) => ref.read(activeClusterProvider);

  @override
  LogSearchState build(String arg) {
    _clusterId = arg;
    initRefreshable(ref);
    return LogSearchState.initial;
  }

  /// Operator pressed Run on the filter bar. Cancels any in-flight
  /// fetch via `supersede()`, marks both panels as loading, and fires
  /// the query + volume requests in parallel.
  Future<void> submit(LogSearchParams params) async {
    if (isDisposed) return;

    // Client-side gate so the operator sees an inline error before
    // the request hits the wire. Backend enforces the same limit;
    // this is a UX shortcut for the paste-a-huge-query case.
    if (params.query.length > kLokiMaxQueryChars) {
      state = state.copyWith(
        params: params,
        result: LogQueryFailed(
          'Query exceeds $kLokiMaxQueryChars characters '
          '(${params.query.length}). Shorten the matcher set or use '
          'search mode to construct a more selective query.',
        ),
        volume: const LogVolumeHidden(),
      );
      return;
    }

    state = state.copyWith(
      params: params,
      result: const LogQueryLoading(),
      volume: const LogVolumeLoading(),
    );
    supersede('new query submitted');
    final captured = captureDispatchId();
    await _runFetches(params, captured);
  }

  /// Re-fires the last submitted query. Used by pull-to-refresh and
  /// the error-card retry button. No-op when no query has run yet.
  Future<void> refresh() async {
    if (isDisposed) return;
    final last = state.params;
    if (last == null) return;
    await submit(last);
  }

  Future<void> _runFetches(LogSearchParams params, int captured) async {
    final repo = ref.read(lokiRepositoryProvider);
    final rangeSec =
        params.range.end.difference(params.range.start).inSeconds.clamp(1, 90 * 24 * 3600);
    final step = chooseVolumeStep(rangeSec);

    // Run query + volume in parallel. Volume is best-effort — its
    // failure mode falls back to a hidden panel rather than a whole-
    // screen error.
    await Future.wait([
      _runQuery(params, captured, repo),
      _runVolume(params, step, captured, repo),
    ]);
  }

  Future<void> _runQuery(
    LogSearchParams params,
    int captured,
    LokiRepository repo,
  ) async {
    try {
      final result = await repo.query(
        query: params.query,
        start: params.range.start,
        end: params.range.end,
        namespace: params.namespace,
        limit: params.limit,
        direction: params.direction,
        clusterIdOverride: _clusterId,
        cancelToken: currentCancelToken,
      );
      if (!isFresh(captured)) return;
      // Pin re-check at result arrival.
      if (!clusterStillPinned(ref)) {
        _writeResult(
          LogQueryFailed(pinnedMismatchMessage(PinPhase.postEmission)),
          captured,
        );
        return;
      }
      _writeResult(LogQueryLoaded(result), captured);
    } on DioException catch (e) {
      if (RefreshableController.isCancelException(e)) return;
      final err = e.error;
      final apiErr = err is ApiError ? err : ApiError.fromDio(e);
      _writeResult(LogQueryFailed(_humanizeQuery(apiErr)), captured);
    } on ArgumentError catch (e) {
      _writeResult(
        LogQueryFailed(e.message?.toString() ?? '$e'),
        captured,
      );
    } on ApiError catch (e) {
      _writeResult(LogQueryFailed(_humanizeQuery(e)), captured);
    } catch (e) {
      _writeResult(LogQueryFailed(e.toString()), captured);
    }
  }

  Future<void> _runVolume(
    LogSearchParams params,
    String step,
    int captured,
    LokiRepository repo,
  ) async {
    try {
      final result = await repo.volume(
        query: params.query,
        start: params.range.start,
        end: params.range.end,
        step: step,
        namespace: params.namespace,
        clusterIdOverride: _clusterId,
        cancelToken: currentCancelToken,
      );
      if (!isFresh(captured)) return;
      if (!clusterStillPinned(ref)) {
        // Volume mismatch is informational — hide the panel rather
        // than surfacing a duplicate cross-cluster banner alongside
        // the results panel's mismatch message.
        _writeVolume(const LogVolumeHidden(), captured);
        return;
      }
      if (result.isEmpty) {
        _writeVolume(const LogVolumeHidden(), captured);
      } else {
        _writeVolume(LogVolumeLoaded(result), captured);
      }
    } on DioException catch (e) {
      if (RefreshableController.isCancelException(e)) return;
      // Volume failure hides the panel — non-critical surface per web
      // parity. The query panel is the operator's actual interest.
      _writeVolume(const LogVolumeHidden(), captured);
    } on ArgumentError {
      _writeVolume(const LogVolumeHidden(), captured);
    } catch (_) {
      _writeVolume(const LogVolumeHidden(), captured);
    }
  }

  void _writeResult(LogQueryStatus status, int captured) {
    if (!isFresh(captured)) return;
    state = state.copyWith(result: status);
  }

  void _writeVolume(LogVolumeStatus status, int captured) {
    if (!isFresh(captured)) return;
    state = state.copyWith(volume: status);
  }

  String _humanizeQuery(ApiError e) {
    if (e.statusCode == 400) {
      // Surface the backend's parse-error verbatim — operators
      // composing LogQL want the original message, not a
      // generic-rewording.
      return e.message;
    }
    if (e.statusCode == 403) {
      return 'Namespace required for log queries. Pick a namespace '
          'or — for cluster-wide reads — open k8sCenter on a '
          'desktop as an admin.';
    }
    if (e.statusCode == 502) {
      return 'Loki query failed (${e.message}). Retry, or shorten '
          'the time range.';
    }
    if (e.statusCode == 503) {
      return 'Loki is not available on this cluster.';
    }
    return e.message;
  }
}

final logSearchControllerProvider = AutoDisposeNotifierProvider.family<
    LogSearchController, LogSearchState, String>(
  LogSearchController.new,
);
