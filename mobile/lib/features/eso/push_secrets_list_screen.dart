// PushSecrets list — the inverse-direction CRD that pushes a
// Kubernetes Secret out to a source store. Read-only in v1 (backend
// has no write surface); mobile mirrors that posture — there are no
// row actions.

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

class PushSecretsListScreen extends ConsumerWidget {
  const PushSecretsListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(esoStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('PushSecrets')),
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

  ExternalSecretListKey get _key =>
      ExternalSecretListKey(clusterId: clusterId);

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(pushSecretListProvider(_key));

    Future<void> handleRefresh() async {
      ref.invalidate(pushSecretListProvider(_key));
      try {
        await ref.read(pushSecretListProvider(_key).future);
      } on Object {/* surfaces via .when */}
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load PushSecrets',
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
                        'No PushSecrets visible. PushSecrets export a '
                        'Kubernetes Secret out to an external store; create '
                        'one via the desktop UI.',
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
            itemBuilder: (context, i) => _PsRow(
              ps: items[i],
              onTap: () => context.push(
                '/clusters/$clusterId/eso/pushsecrets/'
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

class _PsRow extends StatelessWidget {
  const _PsRow({required this.ps, required this.onTap});

  final PushSecret ps;
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
                    ps.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 15,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                EsoStatusPill(status: ps.status, dense: true),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              ps.namespace,
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
            ),
            if (ps.sourceSecretName != null) ...[
              const SizedBox(height: 2),
              Text(
                'source: ${ps.sourceSecretName}',
                style: TextStyle(color: colors.textMuted, fontSize: 11),
                overflow: TextOverflow.ellipsis,
              ),
            ],
            if (ps.storeRefs.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Text(
                  '→ ${ps.storeRefs.length} store${ps.storeRefs.length == 1 ? '' : 's'}',
                  style: TextStyle(color: colors.textMuted, fontSize: 11),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
