// SecretStores list — namespaced stores. Cluster-scoped stores live on
// a parallel screen (`cluster_stores_list_screen.dart`) rather than
// being interleaved here; the desktop UI splits them similarly so
// scope confusion stays uncommon.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'eso_widgets.dart';

class StoresListScreen extends ConsumerWidget {
  const StoresListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(esoStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('SecretStores')),
      body: statusAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ErrorStateView(
          message: e is ApiError ? e.message : e.toString(),
          onRetry: () => ref.invalidate(esoStatusProvider(clusterId)),
        ),
        data: (status) {
          if (!status.detected) return FeatureUnavailableState.eso();
          return _ListBody(clusterId: clusterId);
        },
      ),
    );
  }
}

class _ListBody extends ConsumerWidget {
  const _ListBody({required this.clusterId});

  final String clusterId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(storesListProvider(clusterId));

    Future<void> handleRefresh() async {
      ref.invalidate(storesListProvider(clusterId));
      try {
        await ref.read(storesListProvider(clusterId).future);
      } on Object {/* surfaces via .when */}
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load SecretStores',
          error: e,
          onRetry: handleRefresh,
        ),
        data: (items) {
          if (items.isEmpty) {
            return ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: [
                SizedBox(
                  height: 280,
                  child: Center(
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 32),
                      child: Text(
                        'No SecretStores visible. Create one with the '
                        '"SecretStore" wizard to start syncing secrets.',
                        style: TextStyle(color: colors.textMuted),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                ),
              ],
            );
          }
          return ListView.builder(
            physics: const AlwaysScrollableScrollPhysics(),
            itemCount: items.length,
            itemBuilder: (context, i) => _StoreRow(
              store: items[i],
              onTap: () => context.push(
                '/clusters/$clusterId/eso/stores/'
                '${Uri.encodeComponent(items[i].namespace)}/'
                '${Uri.encodeComponent(items[i].name)}',
              ),
            ),
          );
        },
      ),
    );
  }
}

class _StoreRow extends StatelessWidget {
  const _StoreRow({required this.store, required this.onTap});

  final SecretStore store;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    store.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 15,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                EsoStatusPill(status: store.status, dense: true),
              ],
            ),
            const SizedBox(height: 4),
            Row(
              children: [
                Text(
                  store.namespace,
                  style: TextStyle(color: colors.textSecondary, fontSize: 12),
                ),
                const SizedBox(width: 8),
                ProviderChip(provider: store.provider),
              ],
            ),
            if (store.readyMessage != null && store.readyMessage!.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: Text(
                  store.readyMessage!,
                  style: TextStyle(color: colors.textMuted, fontSize: 11),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
          ],
        ),
      ),
    );
  }
}
