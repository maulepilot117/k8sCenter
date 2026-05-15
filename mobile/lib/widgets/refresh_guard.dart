// In-flight-deduplication guard for `RefreshIndicator.onRefresh`.
//
// Without this guard, two rapid pull-to-refresh gestures stack two
// concurrent `ref.invalidate(...) + await ref.read(...future)` cycles
// against the same provider slot. The first invalidation cancels the
// in-flight CancelToken; the second starts a fresh fetch; the first
// await races the second fetch's completion. The screen flickers
// between loading and data states and may write stale data when the
// race resolves in the wrong order. This mixin short-circuits repeat
// calls while a refresh is mid-flight by handing back the same Future
// rather than spawning a second invalidation.
//
// Lifted from `widgets/domain_list_scaffold.dart`'s private `_inflight`
// field so non-scaffold screens (ESO observatory, future dashboards)
// share the same protection without copy-pasting the implementation.

import 'package:flutter/widgets.dart';

/// Mix into a [ConsumerStatefulWidget] / [State] / [ConsumerWidget]
/// helper class to share a single in-flight refresh future across rapid
/// pull-to-refresh gestures and Retry-button taps.
///
/// Usage:
/// ```dart
/// class _MyBodyState extends ConsumerState<_MyBody> with RefreshGuardMixin {
///   Future<void> _handleRefresh() => guardedRefresh(_runRefresh);
///
///   Future<void> _runRefresh() async {
///     ref.invalidate(myProvider);
///     try {
///       await ref.read(myProvider.future);
///     } on Object {/* surfaces via .when */}
///   }
/// }
/// ```
///
/// The guard is **only** for the orchestrating wrapper; the underlying
/// fetch still proceeds normally. When a second pull arrives while the
/// first is still running, the caller receives the same Future and
/// `RefreshIndicator` correctly waits for that single completion.
mixin RefreshGuardMixin<T extends StatefulWidget> on State<T> {
  Future<void>? _inflight;

  /// Invokes [runner] only when no other guarded refresh is in flight
  /// on this State; otherwise returns the in-flight future verbatim.
  /// Clears the guard on completion regardless of success/failure so a
  /// subsequent refresh can run.
  Future<void> guardedRefresh(Future<void> Function() runner) {
    final pending = _inflight;
    if (pending != null) return pending;
    final fut = runner();
    _inflight = fut;
    fut.whenComplete(() {
      // Only clear if this is still the current in-flight future —
      // dispose() may race the completion in tests.
      if (mounted && identical(_inflight, fut)) _inflight = null;
    });
    return fut;
  }
}
