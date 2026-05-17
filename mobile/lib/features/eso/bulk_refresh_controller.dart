// BulkRefreshController — walks the 8-phase modal sheet for ESO bulk
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
//   3. preview     — render the count + per-namespace breakdown +
//                    RBAC restriction notice. "Continue" advances to
//                    `confirm` (which triggers the shared
//                    `showConfirmSheet` type-to-confirm modal).
//   4. confirm     — type-to-confirm REFRESH via the shared
//                    `confirm_sheet.dart` modal stacked on top. The
//                    body keeps rendering the preview content so a
//                    cancel from the inner sheet drops back cleanly.
//                    On confirmation the controller fires submit.
//   5. submit      — POST `refresh-all` (with the pinned targetUIDs).
//                    Transient; resolves to poll on 202 or error on
//                    409/501/503/etc.
//   6. poll        — GET `bulk-refresh-jobs/{jobId}` every 2s until
//                    `completedAt` lands.
//   7. done        — terminal success surface; summary + outcome lists.
//   8. error       — terminal failure surface; error copy + retry.
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
// cluster) at construction, and every wire call forwards it. The poll
// loop runs against the cluster that was pinned at controller
// construction — the active cluster can change without affecting the
// poll because sheet dismissal (autoDispose) is the teardown signal
// rather than a per-tick active-cluster re-check. The
// active-cluster-still-pinned re-check lives on beginScopeLoad and
// submit, where a mismatch would cause the next request to land on
// the wrong cluster's data. The worker continues server-side; dismissing
// the sheet is non-destructive.

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
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

/// Phases of the modal sheet. Drives the UI's body switcher. See the
/// file-top doc comment for the meaning of each phase.
enum BulkRefreshPhase {
  scopePick,
  scopeLoad,
  preview,
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

  /// [copyWith] uses a `?? this.field` fall-through for every nullable
  /// field by default, which means passing `null` to the named parameter
  /// is indistinguishable from omitting it — both keep the current value.
  /// To explicitly clear a nullable field, set the matching `clearXxx`
  /// sentinel to `true`. The sentinel wins over the value parameter, so
  /// `copyWith(scope: foo, clearScope: true)` clears `scope` to null.
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
    bool clearScope = false,
    bool clearScopeResponse = false,
    bool clearJobId = false,
  }) {
    return BulkRefreshSheetState(
      phase: phase ?? this.phase,
      scope: clearScope ? null : (scope ?? this.scope),
      scopeResponse:
          clearScopeResponse ? null : (scopeResponse ?? this.scopeResponse),
      jobId: clearJobId ? null : (jobId ?? this.jobId),
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

  /// Reentrancy guard for [_poll]. Timer.periodic fires every 2s
  /// regardless of whether the previous tick's GET has returned. On a
  /// slow backend this races: tick 2 starts before tick 1 returns, both
  /// land near-simultaneously, and both try to write to state.
  /// `_pollInFlight` lets tick 2 (and 3, etc.) early-return until tick 1
  /// has finished its turn at the state setter.
  bool _pollInFlight = false;

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
  ///
  /// [preserveError] keeps the previous [errorMessage] visible across
  /// the transition. Used by the 409 `scope_changed` recovery path so
  /// the "Scope changed since you confirmed" banner survives the
  /// auto-re-resolve; the default behavior (false) clears any stale
  /// error so a fresh scope load starts clean.
  Future<void> beginScopeLoad(
    BulkRefreshScope scope, {
    bool preserveError = false,
  }) async {
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
        clearScopeResponse: true,
        clearError: !preserveError,
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
        phase: BulkRefreshPhase.preview,
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

  /// Phase 3 → 4 (preview → confirm). Operator tapped "Continue" on the
  /// preview body; the sheet is about to open the shared
  /// `confirm_sheet.dart` type-to-confirm modal on top. The body keeps
  /// rendering the preview content so a cancel-from-inner-sheet drops
  /// back to a fully-rendered preview rather than a blank flash.
  void confirmPreview() {
    if (isDisposed) return;
    if (state.phase != BulkRefreshPhase.preview) return;
    _setState(state.copyWith(phase: BulkRefreshPhase.confirm));
  }

  /// Phase 4 → 3 (confirm → preview). Called by the sheet when the
  /// stacked confirm modal returns false / null (operator cancelled).
  void backToPreview() {
    if (isDisposed) return;
    if (state.phase != BulkRefreshPhase.confirm) return;
    _setState(state.copyWith(phase: BulkRefreshPhase.preview));
  }

  /// Phase 4 → 5. Operator passed the type-to-confirm; fire the POST.
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

  /// Test seam — production teardown happens via `ref.onDispose(_cancelPoll)`.
  /// Kept available so controller-level tests can deterministically stop
  /// the periodic timer without disposing the whole notifier.
  @visibleForTesting
  void cancelPoll() {
    if (isDisposed) return;
    _cancelPoll();
  }

  /// One-shot poll fired from the AppLifecycleListener on iOS resume.
  /// Re-using `_poll()` means the existing `_pollInFlight` reentrancy
  /// guard naturally drops the resume tick on the floor if the periodic
  /// timer was already mid-fetch. Safe to call when phase != poll; the
  /// inner post-await guard early-returns.
  ///
  /// iOS suspends the periodic Timer when the app backgrounds; without
  /// this nudge the progress bar can sit stale for up to 2s after the
  /// app comes back to the foreground.
  Future<void> pollNow() async {
    if (isDisposed) return;
    if (state.phase != BulkRefreshPhase.poll) return;
    await _poll();
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
          clearScopeResponse: true,
          errorMessage:
              'Scope changed since you confirmed — re-resolving and re-asking.',
        ));
        // Fire-and-forget; same scope, fresh resolve. preserveError
        // keeps the explanation banner visible across the auto-re-
        // resolve so the operator understands why they're being asked
        // to confirm again.
        unawaited(beginScopeLoad(scope, preserveError: true));
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
    // Reentrancy guard — if the previous tick is still awaiting its
    // GET, drop this tick on the floor. The next Timer.periodic fire
    // (or the in-flight one's _setState landing) keeps the loop alive.
    if (_pollInFlight) return;
    _pollInFlight = true;
    try {
      final jobId = state.jobId;
      if (jobId == null) return;
      try {
        final job = await ref.read(esoRepositoryProvider).getBulkRefreshJob(
              jobId: jobId,
              clusterIdOverride: _clusterId,
              cancelToken: currentCancelToken,
              receiveTimeout: pollReceiveTimeout,
            );
        if (isDisposed) return;
        // Post-await phase guard — a late-arriving response must NOT
        // clobber a terminal Done/Error state that an earlier tick (or
        // _emitError) has already landed. Without this, a tick-1 slow
        // "still in progress" response can downgrade a tick-2 Done.
        if (state.phase != BulkRefreshPhase.poll) return;
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
        if (isDisposed) return;
        // Same post-await guard for the error path — if we already
        // emitted a terminal Done/Error, don't downgrade it to "retrying".
        if (state.phase != BulkRefreshPhase.poll) return;
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
        if (isDisposed) return;
        if (state.phase != BulkRefreshPhase.poll) return;
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
    } finally {
      _pollInFlight = false;
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
