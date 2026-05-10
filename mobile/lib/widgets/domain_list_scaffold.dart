// Generic list body used by every M4 CRD-discovered domain screen
// (cert-manager, ESO, policy, GitOps, mesh, scanning). Mirrors
// ResourceListScaffold but operates over arbitrary FutureProviders
// rather than the /v1/resources/ endpoint, so cert lists, violation
// lists, and app lists can all share the same loading/error/empty UX.
//
// The `notDetectedFallback` slot is for "feature not installed" states
// — callers pass a FeatureUnavailableState when the domain status
// endpoint returned `detected: false`, keeping the scaffold unaware of
// the per-domain detection logic.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../theme/kube_theme_builder.dart';
import 'empty_states.dart';

/// A pull-to-refresh list scaffold for any domain-specific
/// `AutoDisposeFutureProvider<List<T>>`. The autoDispose-typed bound
/// is what every M4 domain repository emits (their providers are
/// keyed on `(clusterId, namespace?)` and tear down on screen exit),
/// and it gives the scaffold access to `.future` so the
/// RefreshIndicator can `await` the actual fetch completion instead
/// of guessing with a fixed delay.
class DomainListScaffold<T> extends ConsumerStatefulWidget {
  const DomainListScaffold({
    required this.listProvider,
    required this.itemBuilder,
    this.emptyMessage,
    this.notDetectedFallback,
    this.onRefresh,
    super.key,
  });

  /// AutoDispose-typed so the scaffold can read `.future` for the
  /// pull-to-refresh wait. Callers pass either a bare provider or a
  /// family invocation (`certListProvider(clusterId)`).
  final AutoDisposeFutureProvider<List<T>> listProvider;
  final Widget Function(BuildContext context, T item) itemBuilder;

  /// Text shown in [EmptyState] when the list is empty and
  /// [notDetectedFallback] is null.
  final String? emptyMessage;

  /// Widget shown in place of the empty state when the feature is not
  /// detected on this cluster (callers pass [FeatureUnavailableState]).
  final Widget? notDetectedFallback;

  /// Custom refresh callback. When null, defaults to invalidating the
  /// provider and awaiting its `.future` so the spinner stays up
  /// until data actually arrives (or the call errors).
  final Future<void> Function()? onRefresh;

  @override
  ConsumerState<DomainListScaffold<T>> createState() =>
      _DomainListScaffoldState<T>();
}

class _DomainListScaffoldState<T>
    extends ConsumerState<DomainListScaffold<T>> {
  /// In-flight refresh future. Subsequent pulls await this future
  /// instead of invalidating again, so chained pulls + retry-button
  /// taps + navigation-away cannot stack three or four invalidations
  /// against the same provider while one is mid-flight.
  Future<void>? _inflight;

  Future<void> _doRefresh() {
    final pending = _inflight;
    if (pending != null) return pending;
    final fut = _runRefresh();
    _inflight = fut;
    fut.whenComplete(() {
      if (mounted && identical(_inflight, fut)) _inflight = null;
    });
    return fut;
  }

  Future<void> _runRefresh() async {
    final onRefresh = widget.onRefresh;
    if (onRefresh != null) {
      await onRefresh();
      return;
    }
    ref.invalidate(widget.listProvider);
    try {
      await ref.read(widget.listProvider.future);
    } on Object {
      // Error state surfaces through the .when branch; swallow here
      // to keep RefreshIndicator from rethrowing.
    }
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(widget.listProvider);

    return RefreshIndicator(
      onRefresh: _doRefresh,
      child: async.when(
        loading: () => const _ScrollableShell(child: LoadingState()),
        error: (e, _) => _ScrollableShell(
          child: ErrorStateView(
            message: e.toString(),
            onRetry: _doRefresh,
          ),
        ),
        data: (items) {
          if (items.isEmpty) {
            return _ScrollableShell(
              child: widget.notDetectedFallback ??
                  EmptyState(
                    title: widget.emptyMessage ?? 'No items found',
                    icon: Icons.inbox_outlined,
                  ),
            );
          }
          return ListView.builder(
            physics: const AlwaysScrollableScrollPhysics(),
            itemCount: items.length,
            itemBuilder: (context, i) {
              final item = items[i];
              return Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  widget.itemBuilder(context, item),
                  Divider(
                    color: colors.borderSubtle,
                    height: 1,
                    indent: 16,
                    endIndent: 16,
                  ),
                ],
              );
            },
          );
        },
      ),
    );
  }
}

/// Wraps a non-scrollable child in a ListView so RefreshIndicator has
/// scroll physics to attach to. Same pattern as ResourceListScaffold.
class _ScrollableShell extends StatelessWidget {
  const _ScrollableShell({required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [SizedBox(height: 280, child: child)],
    );
  }
}
