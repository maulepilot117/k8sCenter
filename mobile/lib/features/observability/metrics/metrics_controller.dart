// Controller behind the Metrics tab. Owns the time range, fires one
// `query_range` per panel, and surfaces per-panel state so the chart
// grid can render partial success (one panel 502s, others render).
//
// Race protection comes from the `RefreshableController` mixin:
//   * supersede() rotates the CancelToken + bumps the dispatch id on
//     every time-range change so an in-flight 7d fetch can't write
//     over a fresh 1h selection.
//   * Cluster pin is re-checked at result arrival (postEmission). The
//     wire request itself was already pinned via `X-Cluster-ID`, so a
//     post-emission mismatch surfaces the "request landed on pinned
//     cluster" copy rather than silently writing a different cluster's
//     metrics under the active cluster's screen.
//   * isDisposed gates every post-await state write.
//
// The controller is keyed on the full target (cluster + kind + ns +
// name) so navigating between two pod detail screens doesn't reuse
// the previous pod's series under the new screen — Riverpod's
// autoDispose family handles the cache slot, the controller just
// has to honor the key.

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/api_error.dart';
import '../../../api/monitoring_repository.dart';
import '../../../cluster/cluster_provider.dart';
import '../../../widgets/refreshable_controller.dart';
import 'metric_panels.dart';

/// Composite key for the metrics controller family. Mirrors
/// `ResourceGetKey` shape; carries `clusterId` so cluster swaps don't
/// reuse the prior cluster's slot.
class MetricsTarget {
  const MetricsTarget({
    required this.clusterId,
    required this.kind,
    required this.namespace,
    required this.name,
  });

  final String clusterId;

  /// Backend-canonical plural kind (e.g. "pods", "persistentvolumeclaims").
  final String kind;

  /// Empty for cluster-scoped kinds like "nodes".
  final String namespace;

  final String name;

  @override
  bool operator ==(Object other) =>
      other is MetricsTarget &&
      other.clusterId == clusterId &&
      other.kind == kind &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, kind, namespace, name);
}

/// Mirror of the user-facing TimePreset to keep the controller free of
/// widget imports. The tab translates the picker's enum into this one.
enum MetricsPreset { last15m, last1h, last6h, last24h, last7d, custom }

/// Initial time window picked when the tab first mounts. 1h matches
/// the web's `PromQLQuery.tsx` default and covers the median oncall
/// "is this thing in trouble right now" sweep.
({DateTime start, DateTime end, MetricsPreset preset}) _defaultRange() {
  final end = DateTime.now();
  return (
    start: end.subtract(const Duration(hours: 1)),
    end: end,
    preset: MetricsPreset.last1h,
  );
}

/// Snapshot of one panel's fetch status.
sealed class PanelStatus {
  const PanelStatus();
}

class PanelLoading extends PanelStatus {
  const PanelLoading();
}

class PanelLoaded extends PanelStatus {
  const PanelLoaded(this.result);
  final QueryRangeResult result;
}

class PanelFailed extends PanelStatus {
  const PanelFailed(this.message);
  final String message;
}

/// State the Metrics tab consumes. `cancelled` flips while a refresh
/// is in flight so the picker can disable presets until the
/// supersession resolves (without it, a fast-tapping operator can
/// queue six concurrent fetches that all land in the wrong order).
class MetricsState {
  const MetricsState({
    required this.range,
    required this.panels,
  });

  final ({DateTime start, DateTime end, MetricsPreset preset}) range;
  final Map<String, PanelStatus> panels;

  MetricsState copyWith({
    ({DateTime start, DateTime end, MetricsPreset preset})? range,
    Map<String, PanelStatus>? panels,
  }) {
    return MetricsState(
      range: range ?? this.range,
      panels: panels ?? this.panels,
    );
  }
}

class MetricsController
    extends AutoDisposeFamilyNotifier<MetricsState, MetricsTarget>
    with RefreshableController {
  late MetricsTarget _target;

  @override
  String get pinnedClusterId => _target.clusterId;

  @override
  String currentActiveClusterId(Ref ref) =>
      ref.read(activeClusterProvider);

  @override
  MetricsState build(MetricsTarget arg) {
    _target = arg;
    initRefreshable(ref);
    final initialRange = _defaultRange();
    final panels = metricPanelsByKind[arg.kind] ?? const <MetricPanel>[];
    final initialStatuses = <String, PanelStatus>{
      for (final p in panels) p.id: const PanelLoading(),
    };
    // Schedule the fetch via microtask so Riverpod has committed the
    // initial state before `_writePanel` reads it back. Without this
    // yield, the first await in `_fetchAll` can land before `state` is
    // observable, and the per-panel writes silently drop.
    //
    // Capture the dispatch id eagerly so a same-tick `setRange()` or
    // `refresh()` call (which bumps `_dispatchId` via `supersede`)
    // invalidates this build's fetch before it launches — without
    // this guard, the initial 1h range fetch could still produce the
    // winning result for a user-selected 24h range.
    final captured = captureDispatchId();
    scheduleMicrotask(() {
      if (isDisposed || !isFresh(captured)) return;
      unawaited(_fetchAll(panels, initialRange, capturedOverride: captured));
    });
    return MetricsState(range: initialRange, panels: initialStatuses);
  }

  /// Operator picked a new time range. Cancels every in-flight panel
  /// fetch via `supersede()` and re-fires with the new range.
  void setRange(DateTime start, DateTime end, MetricsPreset preset) {
    if (isDisposed) return;
    final next = (start: start, end: end, preset: preset);
    final panels = metricPanelsByKind[_target.kind] ?? const <MetricPanel>[];
    state = MetricsState(
      range: next,
      panels: {for (final p in panels) p.id: const PanelLoading()},
    );
    supersede('time-range changed');
    unawaited(_fetchAll(panels, next));
  }

  /// Re-fires every panel with the current range. Used by pull-to-
  /// refresh and the error-card retry button.
  Future<void> refresh() async {
    if (isDisposed) return;
    final range = state.range;
    final panels = metricPanelsByKind[_target.kind] ?? const <MetricPanel>[];
    state = MetricsState(
      range: range,
      panels: {for (final p in panels) p.id: const PanelLoading()},
    );
    supersede('manual refresh');
    await _fetchAll(panels, range);
  }

  Future<void> _fetchAll(
    List<MetricPanel> panels,
    ({DateTime start, DateTime end, MetricsPreset preset}) range, {
    int? capturedOverride,
  }) async {
    if (panels.isEmpty) return;
    final captured = capturedOverride ?? captureDispatchId();
    final repo = ref.read(monitoringRepositoryProvider);
    final rangeSec =
        range.end.difference(range.start).inSeconds.clamp(1, 365 * 24 * 3600);
    final step = MonitoringRepository.computeStep(rangeSec);
    final vars = _resolveVariables();

    await Future.wait(panels.map((panel) async {
      String query;
      try {
        query = panel.render(vars);
      } on ArgumentError catch (e) {
        _writePanel(panel.id, PanelFailed(e.message?.toString() ?? '$e'),
            captured);
        return;
      }

      try {
        final result = await repo.queryRange(
          query: query,
          start: range.start,
          end: range.end,
          stepSeconds: step,
          clusterIdOverride: _target.clusterId,
          cancelToken: currentCancelToken,
        );
        // Pin re-check at result arrival — defends against the
        // cluster-switch-mid-fetch race even though the wire request
        // already carried the pinned X-Cluster-ID header.
        if (!clusterStillPinned(ref)) {
          _writePanel(
            panel.id,
            PanelFailed(pinnedMismatchMessage(PinPhase.postEmission)),
            captured,
          );
          return;
        }
        _writePanel(panel.id, PanelLoaded(result), captured);
      } on DioException catch (e) {
        if (RefreshableController.isCancelException(e)) return;
        final err = e.error;
        final apiErr = err is ApiError ? err : ApiError.fromDio(e);
        _writePanel(
          panel.id,
          PanelFailed(_humanize(apiErr)),
          captured,
        );
      } on ApiError catch (e) {
        _writePanel(panel.id, PanelFailed(_humanize(e)), captured);
      } catch (e) {
        _writePanel(panel.id, PanelFailed(e.toString()), captured);
      }
    }));
  }

  /// Translates the controller target into the variable bindings the
  /// per-kind panels declare. `node` panels read the resource name as
  /// the node label value; PVC panels read it into `pvc`; workload
  /// panels follow `<kind-singular>` (deployment / statefulset /
  /// daemonset).
  Map<String, String> _resolveVariables() {
    final base = <String, String>{
      'namespace': _target.namespace,
    };
    switch (_target.kind) {
      case 'pods':
        return {...base, 'pod': _target.name};
      case 'nodes':
        return {'node': _target.name};
      case 'deployments':
        return {...base, 'deployment': _target.name};
      case 'statefulsets':
        return {...base, 'statefulset': _target.name};
      case 'daemonsets':
        return {...base, 'daemonset': _target.name};
      case 'persistentvolumeclaims':
        return {...base, 'pvc': _target.name};
    }
    return base;
  }

  void _writePanel(String panelId, PanelStatus status, int captured) {
    // `isFresh` already short-circuits when `_disposed` is true, so a
    // separate `isDisposed` check would be dead code.
    if (!isFresh(captured)) return;
    final current = state;
    final next = Map<String, PanelStatus>.from(current.panels);
    next[panelId] = status;
    state = current.copyWith(panels: next);
  }

  String _humanize(ApiError e) {
    if (e.statusCode == 502) {
      return 'Prometheus query failed (${e.message}). Retry, or shorten '
          'the time range.';
    }
    if (e.statusCode == 503) {
      return 'Prometheus is not available on this cluster.';
    }
    return e.message;
  }
}

final metricsControllerProvider = AutoDisposeNotifierProvider.family<
    MetricsController, MetricsState, MetricsTarget>(
  MetricsController.new,
);

