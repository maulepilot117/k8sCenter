// Controller behind the per-resource Diagnostics screen. Wraps the
// rules-engine + blast-radius response in `AsyncValue<DiagnosticResponse>`
// so the screen can render loading / error / data uniformly through the
// existing `LoadingState` + `ErrorStateView` widgets.
//
// Race protection comes from `RefreshableController`:
//   * `supersede()` rotates the CancelToken + bumps the dispatch id on
//     every refresh so the backend's 15s context timeout can never
//     write back into the slot of a later, fresher request.
//   * Cluster pin is re-checked at result arrival (postEmission). The
//     wire request already carried `X-Cluster-ID`, so a postEmission
//     mismatch surfaces the "request landed on pinned cluster" copy
//     rather than silently writing the wrong cluster's diagnostics.
//   * `isDisposed` gates every post-await state write.
//
// The controller is keyed on `DiagnosticTarget` (cluster + ns + kind +
// name) so navigating between two pod screens drops the previous
// fetch's result rather than letting it bleed into the new screen.

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/api_error.dart';
import '../../../api/diagnostics_repository.dart';
import '../../../cluster/cluster_provider.dart';
import '../../../widgets/refreshable_controller.dart';

class DiagnosticsController
    extends AutoDisposeFamilyNotifier<AsyncValue<DiagnosticResponse>,
        DiagnosticTarget> with RefreshableController {
  late DiagnosticTarget _target;

  @override
  String get pinnedClusterId => _target.clusterId;

  @override
  String currentActiveClusterId(Ref ref) =>
      ref.read(activeClusterProvider);

  @override
  AsyncValue<DiagnosticResponse> build(DiagnosticTarget arg) {
    _target = arg;
    initRefreshable(ref);

    // Eager captureDispatchId — defends the initial fetch against an
    // immediate `refresh()` from a same-frame caller (e.g. a navigator
    // pushing the screen and pulling-to-refresh in one motion).
    final captured = captureDispatchId();
    scheduleMicrotask(() {
      if (isDisposed || !isFresh(captured)) return;
      unawaited(_fetch(captured: captured));
    });
    return const AsyncValue<DiagnosticResponse>.loading();
  }

  /// Re-fires the fetch with the same target. Used by pull-to-refresh
  /// and by the error card's Retry button.
  Future<void> refresh() async {
    if (isDisposed) return;
    state = const AsyncValue<DiagnosticResponse>.loading();
    supersede('manual refresh');
    await _fetch();
  }

  Future<void> _fetch({int? captured}) async {
    final captureId = captured ?? captureDispatchId();
    final repo = ref.read(diagnosticsRepositoryProvider);
    try {
      final result = await repo.loadDiagnostics(
        namespace: _target.namespace,
        kind: _target.kind,
        name: _target.name,
        clusterIdOverride: _target.clusterId,
        cancelToken: currentCancelToken,
      );
      if (!isFresh(captureId)) return;
      if (!clusterStillPinned(ref)) {
        final msg = pinnedMismatchMessage(PinPhase.postEmission);
        RefreshableController.safeSetIfAlive<AsyncValue<DiagnosticResponse>>(
          isDisposed,
          (s) => state = s,
          AsyncValue.error(
            ApiError(statusCode: 409, code: 409, message: msg),
            StackTrace.current,
          ),
        );
        return;
      }
      RefreshableController.safeSetIfAlive<AsyncValue<DiagnosticResponse>>(
        isDisposed,
        (s) => state = s,
        AsyncValue.data(result),
      );
    } on DioException catch (e, st) {
      if (RefreshableController.isCancelException(e)) return;
      if (!isFresh(captureId)) return;
      final err = e.error;
      final apiErr = err is ApiError ? err : ApiError.fromDio(e);
      RefreshableController.safeSetIfAlive<AsyncValue<DiagnosticResponse>>(
        isDisposed,
        (s) => state = s,
        AsyncValue.error(apiErr, st),
      );
    } on ApiError catch (e, st) {
      if (!isFresh(captureId)) return;
      RefreshableController.safeSetIfAlive<AsyncValue<DiagnosticResponse>>(
        isDisposed,
        (s) => state = s,
        AsyncValue.error(e, st),
      );
    } catch (e, st) {
      if (!isFresh(captureId)) return;
      RefreshableController.safeSetIfAlive<AsyncValue<DiagnosticResponse>>(
        isDisposed,
        (s) => state = s,
        AsyncValue.error(e, st),
      );
    }
  }
}

final diagnosticsControllerProvider = AutoDisposeNotifierProvider.family<
    DiagnosticsController,
    AsyncValue<DiagnosticResponse>,
    DiagnosticTarget>(
  DiagnosticsController.new,
);

/// Namespace-summary state. Standalone FutureProvider rather than a
/// dedicated controller because the surface is read-only / non-keyed
/// from the operator's POV — refresh fires via `ref.invalidate`.
final namespaceSummaryProvider = FutureProvider.autoDispose
    .family<NamespaceSummary, ({String clusterId, String namespace})>(
        (ref, key) async {
  // Watch active cluster so autoDispose tears down on switch even though
  // clusterId is in the family key.
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('summary invalidated');
  });
  return ref.read(diagnosticsRepositoryProvider).namespaceSummary(
        namespace: key.namespace,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});
