// Race-protection mixin lifted from `wizards/wizard_controller.dart` for
// per-domain read controllers in M4.
//
// Why a mixin and not a base class: per-domain controllers extend
// `AutoDisposeFamilyNotifier<TState, TKey>` (Riverpod-typed). Single
// inheritance forces them to extend AutoDispose first; the race-
// protection patterns mix in here without claiming the only extension
// slot. The mixin cannot reach `state` from a generic location (the
// Notifier's `state` setter type depends on `TState`), so the
// dispose-guarded write is implemented at the call site — either as
// `if (!isDisposed) state = next;` or via the static
// `RefreshableController.safeSetIfAlive` helper below.
//
// Subclass contract:
//   1. Override `pinnedClusterId` to return the cluster id this
//      controller is keyed on (typically `arg.clusterId`).
//   2. Override `currentActiveClusterId(ref)` to read the active
//      cluster (typically `ref.read(activeClusterProvider)`).
//   3. Call `initRefreshable(ref)` exactly once from `build()` so the
//      `onDispose` hook registers and `_refreshableInitialized` flips.
//      Calling it again is a no-op (idempotent guard).
//   4. Wrap every post-await state write as
//      `if (!isDisposed) state = next;` or
//      `RefreshableController.safeSetIfAlive<TState>(isDisposed,
//      (s) => state = s, next);`. Without the guard, Riverpod throws
//      `StateError` when an async result lands after the notifier has
//      been disposed.
//   5. Pass `currentCancelToken` to every Dio call so disposal cancels
//      in-flight HTTP. Catch `DioException` BEFORE any generic catch
//      and short-circuit on `RefreshableController.isCancelException`
//      so cancelled fetches are silent rather than surfacing as
//      operator-visible errors.
//   6. After every `await` in a fetch path, re-check cluster pin with
//      `clusterStillPinned(ref)`. On mismatch, call
//      `pinnedMismatchMessage(phase)` to get the operator-facing copy
//      and write it into your failure state.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Phase used to tailor the cluster-mismatch message.
enum PinPhase {
  /// Mismatch detected before the HTTP request was emitted.
  preEmission,

  /// Mismatch detected after the request returned. The request was
  /// pinned via `X-Cluster-ID` so any cluster-side data already
  /// reflects the pinned cluster.
  postEmission,
}

mixin RefreshableController {
  int _dispatchId = 0;
  bool _disposed = false;
  bool _refreshableInitialized = false;
  CancelToken? _cancelToken;

  /// Cluster id this controller is keyed on.
  String get pinnedClusterId;

  /// Live cluster id from the active cluster provider.
  String currentActiveClusterId(Ref ref);

  /// Register the dispose hook. Idempotent: calling more than once is a
  /// no-op so a second `build()` invocation (or accidental copy-paste in
  /// a subclass) cannot stack a second `onDispose` callback that would
  /// double-cancel `_cancelToken` after disposal.
  void initRefreshable(Ref ref) {
    if (_refreshableInitialized) return;
    _refreshableInitialized = true;
    ref.onDispose(() {
      _disposed = true;
      _cancelToken?.cancel('controller disposed');
    });
  }

  /// True after the controller's `ref.onDispose` has fired.
  bool get isDisposed => _disposed;

  /// True after `initRefreshable` has run. Subclasses can assert on
  /// this in their fetch helpers.
  bool get isInitialized => _refreshableInitialized;

  /// Captures a fresh dispatch id for an async operation. The async
  /// path stores this and compares before writing back state on
  /// completion.
  int captureDispatchId() => ++_dispatchId;

  /// Returns `true` when [captured] still matches the live dispatch
  /// id and the controller hasn't been disposed.
  bool isFresh(int captured) => !_disposed && captured == _dispatchId;

  /// Bumps `_dispatchId` so any in-flight async result is dropped on
  /// arrival. Call from `refresh()`, form-edit handlers, or any input
  /// change that invalidates a pending fetch.
  void bumpDispatch() {
    _dispatchId++;
  }

  /// CancelToken for in-flight Dio calls. Subclass passes this to
  /// every Dio call so disposal cancels HTTP cleanly.
  CancelToken get currentCancelToken {
    _cancelToken ??= CancelToken();
    return _cancelToken!;
  }

  /// Supersedes any in-flight fetch by cancelling the active
  /// CancelToken, rotating a fresh one, and bumping `_dispatchId`. The
  /// two operations are paired: cancelling without bumping leaves a
  /// window where a stale response (already in the socket buffer when
  /// the cancel arrives) can write back to state; bumping without
  /// cancelling leaves the HTTP request on the wire wasting bandwidth
  /// and backend load.
  ///
  /// Call from `refresh()`, form-edit handlers, or any input change
  /// that invalidates a pending fetch. This is the canonical entry
  /// point — prefer it over calling [bumpDispatch] or [cancelInflight]
  /// directly.
  void supersede([String? reason]) {
    _cancelToken?.cancel(reason ?? 'superseded by new request');
    _cancelToken = CancelToken();
    _dispatchId++;
  }


  /// Returns `true` when the pinned cluster still matches the active
  /// cluster. On mismatch returns `false`; the caller must then call
  /// [pinnedMismatchMessage] with the appropriate [PinPhase] and write
  /// that message into the failure state. The boolean form is
  /// intentional — the mixin cannot type-safely reach the subclass's
  /// `state` setter, so the action half lives at the call site.
  ///
  /// The phase is supplied to [pinnedMismatchMessage], not to this
  /// check, because the data-correctness question (does pinned still
  /// match active?) is phase-independent; only the operator-facing
  /// copy differs.
  bool clusterStillPinned(Ref ref) {
    return currentActiveClusterId(ref) == pinnedClusterId;
  }

  /// Dispose-guarded state writer for subclasses. The mixin cannot
  /// reach `state` from a generic location (the Notifier's `state`
  /// setter type depends on the subclass's `TState` parameter), so the
  /// caller passes a [setter] closure that captures the live setter.
  ///
  /// Usage in a subclass:
  /// ```dart
  /// RefreshableController.safeSetIfAlive<MyState>(
  ///   isDisposed,
  ///   (s) => state = s,
  ///   computedNext,
  /// );
  /// ```
  ///
  /// No-op when [isDisposed] is true. Equivalent to
  /// `if (!isDisposed) setter(value);` and offered as a single static
  /// call so subclass hot paths can keep one line per state write.
  static void safeSetIfAlive<S>(
    bool isDisposed,
    void Function(S value) setter,
    S value,
  ) {
    if (!isDisposed) setter(value);
  }

  /// Phase-specific message body for cluster mismatch.
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

  /// True when [e] is a `DioException` of type `cancel`. Catch
  /// blocks in fetch helpers should short-circuit on this so cancelled
  /// fetches are silent (no error surface) rather than visible as a
  /// failure card. Static so tests and per-domain controllers can
  /// reference it without an instance.
  static bool isCancelException(Object e) =>
      e is DioException && e.type == DioExceptionType.cancel;
}
