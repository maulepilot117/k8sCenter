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

import '../../api/eso_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/refresh_guard.dart';
import 'eso_widgets.dart';

class ClusterExternalSecretsListScreen extends StatelessWidget {
  const ClusterExternalSecretsListScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('ClusterExternalSecrets')),
      body: EsoStatusGate(
        builder: (clusterId) => _ListBody(clusterId: clusterId),
      ),
    );
  }
}

class _ListBody extends ConsumerStatefulWidget {
  const _ListBody({required this.clusterId});

  final String clusterId;

  @override
  ConsumerState<_ListBody> createState() => _ListBodyState();
}

class _ListBodyState extends ConsumerState<_ListBody>
    with RefreshGuardMixin {
  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(
          clusterExternalSecretListProvider(widget.clusterId),
        );
        try {
          await ref.read(
            clusterExternalSecretListProvider(widget.clusterId).future,
          );
        } on Object {/* surfaces via .when */}
      });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async =
        ref.watch(clusterExternalSecretListProvider(widget.clusterId));

    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load ClusterExternalSecrets',
          error: e,
          onRetry: _handleRefresh,
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
                '/clusters/${widget.clusterId}/eso/cluster-externalsecrets/'
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
    // Dedupe: a namespace can appear in both the configured `namespaces`
    // list and `provisionedNamespaces` once the controller has reconciled.
    // Set-union prevents the "12 namespaces" badge from inflating to 24.
    final nsCount =
        {...ces.namespaces, ...ces.provisionedNamespaces}.length;
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
