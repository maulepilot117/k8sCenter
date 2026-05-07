// Wraps a per-kind list screen body with RefreshIndicator + uniform
// loading/error states. Ensures every list screen ships pull-to-refresh
// and a Retry button, fixing the inconsistency across PR-1d's 6 screens
// where only pod_screens and deployment_screens had `onRetry`.
//
// The truncation banner surfaces when the backend paginated and the
// rendered list is a subset of the cluster's actual count — operators
// notice "showing 200 of 537 pods" rather than silently seeing only 200.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/resource_repository.dart';
import '../theme/kube_theme_builder.dart';
import 'empty_states.dart';

class ResourceListScaffold extends ConsumerWidget {
  const ResourceListScaffold({
    super.key,
    required this.providerKey,
    required this.builder,
  });

  final ResourceListKey providerKey;

  /// Renders the table body from the loaded items. Receives the full
  /// `ResourceList` so callers can show the truncation banner / total
  /// count where appropriate.
  final Widget Function(BuildContext context, ResourceList result) builder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final list = ref.watch(resourceListProvider(providerKey));
    final colors = Theme.of(context).extension<KubeColors>()!;

    return RefreshIndicator(
      onRefresh: () async {
        // ref.refresh returns a Future for the new fetch — await it so
        // the indicator stays visible until the data lands.
        // ignore: unused_result
        ref.refresh(resourceListProvider(providerKey));
        await ref.read(resourceListProvider(providerKey).future);
      },
      child: list.when(
        loading: () => const _ScrollableShell(child: LoadingState()),
        error: (e, _) => _ScrollableShell(
          child: ErrorStateView(
            message: e.toString(),
            onRetry: () => ref.invalidate(resourceListProvider(providerKey)),
          ),
        ),
        data: (result) {
          if (!result.truncated) {
            return builder(context, result);
          }
          return Column(
            children: [
              Container(
                width: double.infinity,
                padding:
                    const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                color: colors.warningDim,
                child: Text(
                  'Showing ${result.items.length} of ${result.total}. '
                  'Refine via namespace or labels for the rest.',
                  style: TextStyle(color: colors.warning, fontSize: 12),
                ),
              ),
              Expanded(child: builder(context, result)),
            ],
          );
        },
      ),
    );
  }
}

/// Wraps a non-scrollable child in a `ListView` so `RefreshIndicator`
/// has a scrollable to attach to (pull-to-refresh requires scroll
/// physics). Used for the loading and error branches.
class _ScrollableShell extends StatelessWidget {
  const _ScrollableShell({required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [
        SizedBox(height: 240, child: child),
      ],
    );
  }
}
