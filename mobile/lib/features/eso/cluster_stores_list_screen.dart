// ClusterSecretStores list — cluster-scoped variant of the SecretStore
// list. Same row shape as the namespaced screen, minus the namespace
// label.

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

class ClusterStoresListScreen extends ConsumerWidget {
  const ClusterStoresListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(esoStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('ClusterSecretStores')),
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
    final async = ref.watch(clusterStoresListProvider(clusterId));

    Future<void> handleRefresh() async {
      ref.invalidate(clusterStoresListProvider(clusterId));
      try {
        await ref.read(clusterStoresListProvider(clusterId).future);
      } on Object {/* surfaces via .when */}
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load ClusterSecretStores',
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
                        'No ClusterSecretStores visible. They are cluster-'
                        'scoped — your account may need broader permissions, '
                        'or none exist on this cluster.',
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
                '/clusters/$clusterId/eso/cluster-stores/'
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
                Icon(
                  Icons.public_outlined,
                  size: 16,
                  color: colors.accent,
                ),
                const SizedBox(width: 6),
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
            ProviderChip(provider: store.provider),
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
