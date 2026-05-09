// Race-protection mixin lifted from `wizards/wizard_controller.dart` for
// per-domain read controllers in M4 (metrics, logs, diagnostics, gitops,
// mesh, certificates, ESO, policy, scanning).
//
// Why a mixin and not a base class: per-domain controllers extend
// `AutoDisposeFamilyNotifier<TState, TKey>` (Riverpod-typed). Single
// inheritance forces them to extend AutoDispose first; the race-
// protection patterns then mix in here without claiming the only
// extension slot.
//
// What it carries:
//   * `_dispatchId` — bumped by `refresh()` and any other action that
//     should invalidate an in-flight fetch. Async paths capture the id
//     at start and drop the result on mismatch. Prevents the late-200
//     from a back-out fetch re-routing state forward.
//   * `_disposed` — set by `ref.onDispose`. Every post-await state
//     setter checks via [safeSet] so writing to a torn-down notifier
//     doesn't throw `StateError`.
//   * `_clusterStillPinned(phase)` — pre/post-emission cluster check.
//     Pre-emission mismatches abort cleanly; post-emission mismatches
//     happen *after* a request landed on the pinned cluster's
//     `X-Cluster-ID`, so the failure copy says "the request landed on
//     the pinned cluster" rather than "aborted."
//   * `cancelInflight()` — cancels the active `CancelToken` so cluster
//     switches mid-fetch don't write stale state. Subclasses are
//     responsible for passing `currentCancelToken` to Dio calls.
//
// Subclass contract:
//   1. Override `pinnedClusterId` to return the cluster id this
//      controller is keyed on (typically `arg.clusterId`).
//   2. Override `activeClusterId(WidgetRef-equivalent)` — typically
//      `ref.read(activeClusterProvider)`.
//   3. Call `initRefreshable(ref)` exactly once from `build()` so the
//      `onDispose` hook registers.
//   4. Wrap every state setter in `safeSet(next)`.
//   5. Pass `currentCancelToken` to every Dio call so disposal cancels
//      in-flight HTTP.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Phase used to tailor the cluster-mismatch surfaced message.
enum PinPhase {
  /// Mismatch detected before the HTTP request was emitted.
  preEmission,

  /// Mismatch detected after the request returned. The request was
  /// pinned via `X-Cluster-ID` so any cluster-side mutation already
  /// landed on the pinned cluster.
  postEmission,
}

mixin RefreshableController {
  int _dispatchId = 0;
  bool _disposed = false;
  CancelToken? _cancelToken;

  /// Cluster id this controller is keyed on. Subclass returns
  /// `arg.clusterId` (or equivalent family-key field).
  String get pinnedClusterId;

  /// Live cluster id from the active cluster provider. Subclass reads
  /// `ref.read(activeClusterProvider)` (or equivalent).
  String currentActiveClusterId(Ref ref);

  /// Register the dispose hook. Call once from `build()`.
  void initRefreshable(Ref ref) {
    ref.onDispose(() {
      _disposed = true;
      _cancelToken?.cancel('controller disposed');
    });
  }

  /// True after the controller's `ref.onDispose` has fired.
  bool get isDisposed => _disposed;

  /// Captures a fresh dispatch id for an async operation. The async
  /// path stores this and compares before writing back state on
  /// completion. Bumping the id elsewhere (via [bumpDispatch]) makes
  /// the captured id stale.
  int captureDispatchId() => ++_dispatchId;

  /// Returns `true` when [captured] still matches the live dispatch
  /// id and the controller hasn't been disposed. Async result
  /// handlers gate state writes on this.
  bool isFresh(int captured) => !_disposed && captured == _dispatchId;

  /// Bumps `_dispatchId` so any in-flight async result is dropped on
  /// arrival. Call from `refresh()`, `back()`, form-edit handlers,
  /// or any input change that invalidates a pending fetch.
  void bumpDispatch() {
    _dispatchId++;
  }

  /// CancelToken for in-flight Dio calls. Subclass passes this to
  /// every `dio.get/post(...)` so disposal cancels HTTP cleanly.
  CancelToken get currentCancelToken {
    _cancelToken ??= CancelToken();
    return _cancelToken!;
  }

  /// Cancels the active CancelToken and replaces it with a fresh one.
  /// Called by [refresh] before re-firing a fetch so the previous
  /// request doesn't compete with the new one for state.
  void cancelInflight([String? reason]) {
    _cancelToken?.cancel(reason ?? 'superseded by new request');
    _cancelToken = CancelToken();
  }

  /// Pre/post-emission cluster pin check. Returns `true` when the
  /// pinned cluster still matches the active cluster. On mismatch,
  /// returns `false` and surfaces a phase-specific message via
  /// [onClusterMismatch] (subclass-defined — see [pinnedMismatchMessage]).
  bool clusterStillPinned(Ref ref, {PinPhase phase = PinPhase.preEmission}) {
    final active = currentActiveClusterId(ref);
    if (active == pinnedClusterId) return true;
    return false;
  }

  /// Phase-specific message body for cluster mismatch. Subclass uses
  /// this in its failure-state setter when [clusterStillPinned]
  /// returns false.
  String pinnedMismatchMessage(PinPhase phase) {
    return switch (phase) {
      PinPhase.preEmission =>
        'Cluster changed during this fetch. Aborted to avoid loading '
            "the wrong cluster's data. Refresh from the new cluster.",
      PinPhase.postEmission =>
        'Cluster changed mid-request. The data was loaded from the '
            'pinned cluster ($pinnedClusterId); re-open this surface '
            'from the active cluster to refresh.',
    };
  }
}
