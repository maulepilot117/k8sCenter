// BulkRefreshController — walks the 4-phase modal sheet for ESO bulk
// refresh (Force Sync the entire scope of a SecretStore /
// ClusterSecretStore / Namespace).
//
// Phases (mobile-only adds the first; web's `ESOBulkRefreshDialog.tsx`
// enters at phase 2 because it's launched per-store/per-namespace):
//
//   1. scopePick   — operator picks the variant (store /
//                    clusterStore / namespace) and the identifier.
//                    Lives entirely client-side; no wire activity.
//   2. scopeLoad   — GET the variant's `refresh-scope` endpoint to
//                    enumerate the visible ExternalSecrets.
//   3. confirm     — render the count + per-namespace breakdown and
//                    gate on "type REFRESH to confirm".
//   4. submitPoll  — POST `refresh-all` (with the pinned targetUIDs),
//                    receive jobId, poll `bulk-refresh-jobs/{jobId}`
//                    every 2s until `completedAt` lands.
//
// Special cases:
//   * 409 `active_job_exists` on submit — read `error.extra.jobId` and
//     skip straight to the poll, attaching to the existing job. No
//     re-confirm.
//   * 409 `scope_changed` on submit — drop back to scopeLoad and let
//     the operator re-confirm. (Backend signals UIDs added/removed via
//     `error.extra`; the M5 mobile surface treats that as "scope moved
//     under you" and re-resolves rather than showing the diff inline.)
//   * Poll error (transient) — keep the poll loop running but surface
//     "Retrying…" inline. Falls back to error state if the same path
//     keeps failing.
//   * 5 min total elapsed without completion — flip a flag so the UI
//     can surface "Taking longer than expected"; poll continues until
//     dispose or completion.
//
// Cluster pinning: the scope picker captures `clusterId` (the pinned
// cluster) at construction, and every wire call forwards it. Active-
// cluster switches mid-flow drop the poll loop and surface a clear
// failure (the worker continues server-side; dismissing the sheet is
// non-destructive).

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../widgets/refreshable_controller.dart';

/// Sealed selector of which variant the operator is targeting. Each
/// variant dispatches to a different pair of (scope, submit) endpoints
/// and carries a different identifier shape — store needs ns+name,
/// clusterStore needs name, namespace needs just the namespace.
sealed class BulkRefreshScope {
  const BulkRefreshScope();

  /// Human-readable identifier used in the confirm UI ("store/foo",
  /// "clusterstore/bar", "namespace/prod").
  String displayId();
}

class BulkRefreshScopeStore extends BulkRefreshScope {
  const BulkRefreshScopeStore({required this.namespace, required this.name});

  final String namespace;
  final String name;

  @override
  String displayId() => 'store $namespace/$name';

  @override
  bool operator ==(Object other) =>
      other is BulkRefreshScopeStore &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(namespace, name);
}

class BulkRefreshScopeClusterStore extends BulkRefreshScope {
  const BulkRefreshScopeClusterStore({required this.name});

  final String name;

  @override
  String displayId() => 'cluster store $name';

  @override
  bool operator ==(Object other) =>
      other is BulkRefreshScopeClusterStore && other.name == name;

  @override
  int get hashCode => name.hashCode;
}

class BulkRefreshScopeNamespace extends BulkRefreshScope {
  const BulkRefreshScopeNamespace({required this.namespace});

  final String namespace;

  @override
  String displayId() => 'namespace $namespace';

  @override
  bool operator ==(Object other) =>
      other is BulkRefreshScopeNamespace && other.namespace == namespace;

  @override
  int get hashCode => namespace.hashCode;
}

/// Phases of the modal sheet. Drives the UI's body switcher.
enum BulkRefreshPhase {
  scopePick,
  scopeLoad,
  confirm,
  submit,
  poll,
  done,
  error,
}

/// Whole state object held by the controller. Immutable copies are
/// emitted on every transition so `ref.listen` consumers receive a
/// clean diff.
class BulkRefreshSheetState {
  const BulkRefreshSheetState({
    required this.phase,
    this.scope,
    this.scopeResponse,
    this.jobId,
    this.job,
    this.errorMessage,
    this.takingLong = false,
    this.attachedToExistingJob = false,
    this.pollRetrying = false,
  });

  final BulkRefreshPhase phase;
  final BulkRefreshScope? scope;
  final BulkScopeResponse? scopeResponse;
  final String? jobId;
  final BulkRefreshJob? job;

  /// Non-null in [BulkRefreshPhase.error] and as a transient banner in
  /// [BulkRefreshPhase.poll] when [pollRetrying] is true.
  final String? errorMessage;

  /// True once the poll loop has been running for 5+ minutes without
  /// completion; UI surfaces a "Taking longer than expected" caption.
  final bool takingLong;

  /// True when the submit returned 409 active_job_exists and the
  /// controller attached to an existing jobId rather than starting a
  /// fresh one. UI mentions this on the poll surface so the operator
  /// understands why the count may not match a fresh resolution.
  final bool attachedToExistingJob;

  /// True during a transient poll error (5xx, network) when we're
  /// still retrying. UI surfaces "Retrying…" inline.
  final bool pollRetrying;

  BulkRefreshSheetState copyWith({
    BulkRefreshPhase? phase,
    BulkRefreshScope? scope,
    BulkScopeResponse? scopeResponse,
    String? jobId,
    BulkRefreshJob? job,
    String? errorMessage,
    bool? takingLong,
    bool? attachedToExistingJob,
    bool? pollRetrying,
    bool clearError = false,
    bool clearJob = false,
  }) {
    return BulkRefreshSheetState(
      phase: phase ?? this.phase,
      scope: scope ?? this.scope,
      scopeResponse: scopeResponse ?? this.scopeResponse,
      jobId: jobId ?? this.jobId,
      job: clearJob ? null : (job ?? this.job),
      errorMessage: clearError ? null : (errorMessage ?? this.errorMessage),
      takingLong: takingLong ?? this.takingLong,
      attachedToExistingJob:
          attachedToExistingJob ?? this.attachedToExistingJob,
      pollRetrying: pollRetrying ?? this.pollRetrying,
    );
  }
}

const Duration _pollInterval = Duration(seconds: 2);
const Duration _takingLongThreshold = Duration(minutes: 5);

/// Family-keyed controller. Each open sheet gets its own
/// `clusterId`-keyed slot; closing the sheet disposes the controller
/// and tears down the poll loop.
class BulkRefreshController
    extends AutoDisposeFamilyNotifier<BulkRefreshSheetState, String>
    with RefreshableController {
  late String _clusterId;
  Timer? _pollTimer;
  DateTime? _pollStartedAt;
  int _consecutivePollErrors = 0;

  /// Surfaced as an instance member so tests can override / stub. Bulk
  /// refresh jobs commonly exceed 30s on large scopes, so we tighten
  /// the receive timeout on poll to 10s — enough to absorb backend
  /// scheduling latency without holding the loop hostage to a wedged
  /// kube-apiserver.
  Duration get pollReceiveTimeout => const Duration(seconds: 10);

  @override
  String get pinnedClusterId => _clusterId;

  @override
  String currentActiveClusterId(Ref ref) => ref.read(activeClusterProvider);

  @override
  BulkRefreshSheetState build(String clusterId) {
    _clusterId = clusterId;
    initRefreshable(ref);
    ref.onDispose(_cancelPoll);
    return const BulkRefreshSheetState(phase: BulkRefreshPhase.scopePick);
  }

  /// Phase 1 → 2. Operator confirmed the scope variant + identifier;
  /// fire the GET refresh-scope.
  Future<void> beginScopeLoad(BulkRefreshScope scope) async {
    if (isDisposed) return;
    // Cluster-pin pre-check; cluster switch since the sheet opened
    // means the picker's identifier is for a different cluster's data.
    if (!clusterStillPinned(ref)) {
      _emitError(pinnedMismatchMessage(PinPhase.preEmission));
      return;
    }
    supersede('scope load');
    final captured = captureDispatchId();
    _setState(
      state.copyWith(
        phase: BulkRefreshPhase.scopeLoad,
        scope: scope,
        scopeResponse: null,
        clearError: true,
      ),
    );

    try {
      final repo = ref.read(esoRepositoryProvider);
      final resp = switch (scope) {
        BulkRefreshScopeStore(:final namespace, :final name) =>
          await repo.resolveStoreScope(
            namespace: namespace,
            name: name,
            clusterIdOverride: _clusterId,
            cancelToken: currentCancelToken,
          ),
        BulkRefreshScopeClusterStore(:final name) =>
          await repo.resolveClusterStoreScope(
            name: name,
            clusterIdOverride: _clusterId,
            cancelToken: currentCancelToken,
          ),
        BulkRefreshScopeNamespace(:final namespace) =>
          await repo.resolveNamespaceScope(
            namespace: namespace,
            clusterIdOverride: _clusterId,
            cancelToken: currentCancelToken,
          ),
      };
      if (!isFresh(captured)) return;
      if (!clusterStillPinned(ref)) {
        _emitError(pinnedMismatchMessage(PinPhase.postEmission));
        return;
      }
      _setState(state.copyWith(
        phase: BulkRefreshPhase.confirm,
        scopeResponse: resp,
      ));
    } on DioException catch (e) {
      if (RefreshableController.isCancelException(e)) return;
      if (!isFresh(captured)) return;
      final err = e.error;
      final apiErr = err is ApiError ? err : ApiError.fromDio(e);
      _emitError(apiErr.message);
    } on ApiError catch (e) {
      if (!isFresh(captured)) return;
      _emitError(e.message);
    } catch (e) {
      if (!isFresh(captured)) return;
      _emitError(e.toString());
    }
  }

  /// Step back to phase 1 so the operator can re-pick the variant.
  /// Cancels any in-flight scope-load and clears the previous response.
  void backToScopePick() {
    if (isDisposed) return;
    supersede('back to scope pick');
    _setState(const BulkRefreshSheetState(phase: BulkRefreshPhase.scopePick));
  }

  /// Phase 3 → 4. Operator passed the type-to-confirm; fire the POST.
  Future<void> submit() async {
    if (isDisposed) return;
    if (state.phase != BulkRefreshPhase.confirm) return;
    final scope = state.scope;
    final scopeResp = state.scopeResponse;
    if (scope == null || scopeResp == null) {
      _emitError('Internal error: missing scope on submit');
      return;
    }
    if (!clusterStillPinned(ref)) {
      _emitError(pinnedMismatchMessage(PinPhase.preEmission));
      return;
    }
    supersede('submit');
    final captured = captureDispatchId();
    _setState(state.copyWith(phase: BulkRefreshPhase.submit, clearError: true));

    try {
      final repo = ref.read(esoRepositoryProvider);
      final uids =
          scopeResp.targets.map((t) => t.uid).toList(growable: false);
      final resp = switch (scope) {
        BulkRefreshScopeStore(:final namespace, :final name) =>
          await repo.bulkRefreshStore(
            namespace: namespace,
            name: name,
            targetUIDs: uids,
            clusterIdOverride: _clusterId,
            cancelToken: currentCancelToken,
          ),
        BulkRefreshScopeClusterStore(:final name) =>
          await repo.bulkRefreshClusterStore(
            name: name,
            targetUIDs: uids,
            clusterIdOverride: _clusterId,
            cancelToken: currentCancelToken,
          ),
        BulkRefreshScopeNamespace(:final namespace) =>
          await repo.bulkRefreshNamespace(
            namespace: namespace,
            targetUIDs: uids,
            clusterIdOverride: _clusterId,
            cancelToken: currentCancelToken,
          ),
      };
      if (!isFresh(captured)) return;
      if (!clusterStillPinned(ref)) {
        _emitError(pinnedMismatchMessage(PinPhase.postEmission));
        return;
      }
      _startPoll(jobId: resp.jobId, attached: false);
    } on DioException catch (e) {
      if (RefreshableController.isCancelException(e)) return;
      if (!isFresh(captured)) return;
      final err = e.error;
      final apiErr = err is ApiError ? err : ApiError.fromDio(e);
      _handleSubmitFailure(apiErr);
    } on ApiError catch (e) {
      if (!isFresh(captured)) return;
      _handleSubmitFailure(e);
    } catch (e) {
      if (!isFresh(captured)) return;
      _emitError(e.toString());
    }
  }

  /// Caller (typically the sheet's "Run in background" / dismiss
  /// button) tearing down the poll loop while the worker continues
  /// server-side. The next bulk-refresh-jobs GET keeps server-side
  /// accounting honest.
  void cancelPoll() {
    if (isDisposed) return;
    _cancelPoll();
  }

  void _handleSubmitFailure(ApiError err) {
    // active_job_exists — attach to the existing job and start polling.
    if (err.statusCode == 409 && err.reason == 'active_job_exists') {
      final existingId = err.extraString('jobId');
      if (existingId != null && existingId.isNotEmpty) {
        _startPoll(jobId: existingId, attached: true);
        return;
      }
      _emitError(
        'Another bulk refresh is already in flight for this scope. '
        'Try opening the existing job from the audit log.',
      );
      return;
    }
    // scope_changed — drop back to scope-load so the operator can
    // re-confirm against the freshly-resolved scope.
    if (err.statusCode == 409 && err.reason == 'scope_changed') {
      final scope = state.scope;
      if (scope != null) {
        _setState(state.copyWith(
          phase: BulkRefreshPhase.scopeLoad,
          scopeResponse: null,
          errorMessage:
              'Scope changed since you confirmed — re-resolving and re-asking.',
        ));
        // Fire-and-forget; same scope, fresh resolve.
        unawaited(beginScopeLoad(scope));
        return;
      }
    }
    if (err.statusCode == 501) {
      _emitError(
        "Bulk refresh isn't available for this cluster — only the local "
        'cluster supports ESO writes.',
      );
      return;
    }
    if (err.statusCode == 503) {
      _emitError(
        'ESO is not detected on this cluster. Install ESO before triggering '
        'a bulk refresh.',
      );
      return;
    }
    if (err.statusCode == 413) {
      _emitError(
        'Scope is too large for a single bulk refresh. Use a per-namespace '
        'refresh instead.',
      );
      return;
    }
    if (err.statusCode == 422) {
      _emitError(
        'Nothing to refresh — no ExternalSecrets are in scope or you have '
        'no permission to refresh them.',
      );
      return;
    }
    _emitError(err.message);
  }

  void _startPoll({required String jobId, required bool attached}) {
    _cancelPoll();
    _pollStartedAt = DateTime.now();
    _consecutivePollErrors = 0;
    _setState(state.copyWith(
      phase: BulkRefreshPhase.poll,
      jobId: jobId,
      attachedToExistingJob: attached,
      clearJob: true,
      clearError: true,
      pollRetrying: false,
      takingLong: false,
    ));
    // First poll immediately so the UI gets a count instead of the
    // 2-second blank wait.
    unawaited(_poll());
    _pollTimer = Timer.periodic(_pollInterval, (_) => unawaited(_poll()));
  }

  Future<void> _poll() async {
    if (isDisposed) return;
    final jobId = state.jobId;
    if (jobId == null) return;
    try {
      final job = await ref.read(esoRepositoryProvider).getBulkRefreshJob(
            jobId: jobId,
            clusterIdOverride: _clusterId,
            cancelToken: currentCancelToken,
          );
      if (isDisposed) return;
      _consecutivePollErrors = 0;
      final long = _pollStartedAt != null &&
          DateTime.now().difference(_pollStartedAt!) >= _takingLongThreshold;
      if (job.isDone) {
        _cancelPoll();
        _setState(state.copyWith(
          phase: BulkRefreshPhase.done,
          job: job,
          pollRetrying: false,
          takingLong: long,
          clearError: true,
        ));
        return;
      }
      _setState(state.copyWith(
        job: job,
        pollRetrying: false,
        takingLong: long,
        clearError: true,
      ));
    } on DioException catch (e) {
      if (RefreshableController.isCancelException(e)) return;
      _consecutivePollErrors++;
      // Tolerate transient blips — give up after 3 consecutive
      // failures so the sheet doesn't lie about the job's state
      // forever.
      if (_consecutivePollErrors >= 3) {
        _cancelPoll();
        final err = e.error;
        final apiErr = err is ApiError ? err : ApiError.fromDio(e);
        _emitError(
          'Lost track of the refresh job: ${apiErr.message}. The job may '
          'still complete server-side; check the audit log for status.',
        );
        return;
      }
      _setState(state.copyWith(pollRetrying: true));
    } catch (_) {
      // Same fall-through as DioException for non-Dio errors.
      _consecutivePollErrors++;
      if (_consecutivePollErrors >= 3) {
        _cancelPoll();
        _emitError(
          'Lost track of the refresh job. It may still complete '
          'server-side; check the audit log.',
        );
        return;
      }
      _setState(state.copyWith(pollRetrying: true));
    }
  }

  void _cancelPoll() {
    _pollTimer?.cancel();
    _pollTimer = null;
  }

  void _emitError(String message) {
    _cancelPoll();
    _setState(state.copyWith(
      phase: BulkRefreshPhase.error,
      errorMessage: message,
    ));
  }

  void _setState(BulkRefreshSheetState next) {
    RefreshableController.safeSetIfAlive<BulkRefreshSheetState>(
      isDisposed,
      (s) => state = s,
      next,
    );
  }
}

final bulkRefreshControllerProvider = AutoDisposeNotifierProvider.family<
    BulkRefreshController, BulkRefreshSheetState, String>(
  BulkRefreshController.new,
);
