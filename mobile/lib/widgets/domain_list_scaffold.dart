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
/// `FutureProvider<List<T>>`. The provider type is expressed as a
/// `ProviderBase<AsyncValue<List<T>>>` so callers can pass both
/// non-family providers and the result of calling a family provider
/// (e.g. `certListProvider(clusterId)`). `ProviderBase` satisfies both
/// `ProviderListenable` (for `ref.watch`) and `ProviderOrFamily` (for
/// `ref.invalidate`), so the scaffold can do both without a separate
/// invalidation callback.
class DomainListScaffold<T> extends ConsumerWidget {
  const DomainListScaffold({
    required this.listProvider,
    required this.itemBuilder,
    this.emptyMessage,
    this.notDetectedFallback,
    this.onRefresh,
    super.key,
  });

  final ProviderBase<AsyncValue<List<T>>> listProvider;
  final Widget Function(BuildContext context, T item) itemBuilder;

  /// Text shown in [EmptyState] when the list is empty and
  /// [notDetectedFallback] is null.
  final String? emptyMessage;

  /// Widget shown in place of the empty state when the feature is not
  /// detected on this cluster (callers pass [FeatureUnavailableState]).
  final Widget? notDetectedFallback;

  /// Custom refresh callback. When null, defaults to
  /// `ref.invalidate(listProvider)`.
  final Future<void> Function()? onRefresh;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(listProvider);

    Future<void> doRefresh() async {
      if (onRefresh != null) {
        await onRefresh!();
      } else {
        ref.invalidate(listProvider);
        // Give the invalidated provider a tick to start fetching so the
        // RefreshIndicator doesn't dismiss before data arrives.
        await Future<void>.delayed(const Duration(milliseconds: 100));
      }
    }

    return RefreshIndicator(
      onRefresh: doRefresh,
      child: async.when(
        loading: () => const _ScrollableShell(child: LoadingState()),
        error: (e, _) => _ScrollableShell(
          child: ErrorStateView(
            message: e.toString(),
            onRetry: () => ref.invalidate(listProvider),
          ),
        ),
        data: (items) {
          if (items.isEmpty) {
            return _ScrollableShell(
              child: notDetectedFallback ??
                  EmptyState(
                    title: emptyMessage ?? 'No items found',
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
                  itemBuilder(context, item),
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
