// ClusterExternalSecrets list — cluster-scoped fan-out form. Backend
// returns an empty list when the impersonated user lacks
// `list clusterexternalsecrets`, so non-admin operators see a clean
// "no entries" state rather than a 403 toast.
//
// Drift is intentionally absent here — the backend doesn't track
// drift on cluster-scoped variants (it's a property of each fanned-out
// child ES, not the parent CES).

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

class ClusterExternalSecretsListScreen extends ConsumerWidget {
  const ClusterExternalSecretsListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(esoStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('ClusterExternalSecrets')),
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
    final async = ref.watch(clusterExternalSecretListProvider(clusterId));

    Future<void> handleRefresh() async {
      ref.invalidate(clusterExternalSecretListProvider(clusterId));
      try {
        await ref.read(clusterExternalSecretListProvider(clusterId).future);
      } on Object {/* surfaces via .when */}
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load ClusterExternalSecrets',
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
                        'No ClusterExternalSecrets visible. They are cluster-'
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
            itemBuilder: (context, i) => _CesRow(
              ces: items[i],
              onTap: () => context.push(
                '/clusters/$clusterId/eso/cluster-externalsecrets/'
                '${Uri.encodeComponent(items[i].name)}',
              ),
            ),
          );
        },
      ),
    );
  }
}

class _CesRow extends StatelessWidget {
  const _CesRow({required this.ces, required this.onTap});

  final ClusterExternalSecret ces;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final nsCount = ces.namespaces.length + ces.provisionedNamespaces.length;
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
                    ces.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 15,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                EsoStatusPill(status: ces.status, dense: true),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              '${ces.storeRef.kind}/${ces.storeRef.name}',
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
              overflow: TextOverflow.ellipsis,
            ),
            if (nsCount > 0) ...[
              const SizedBox(height: 2),
              Text(
                '$nsCount namespace${nsCount == 1 ? '' : 's'}'
                "${ces.failedNamespaces.isEmpty ? '' : '  ·  ${ces.failedNamespaces.length} failed'}",
                style: TextStyle(
                  color: ces.failedNamespaces.isEmpty
                      ? colors.textMuted
                      : colors.warning,
                  fontSize: 11,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
