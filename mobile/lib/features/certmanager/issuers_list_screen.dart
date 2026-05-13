// Issuers list — combined Issuers + ClusterIssuers rendered as a flat
// list with scope/type/ready columns. Mirrors the web's
// `frontend/islands/IssuersList.tsx` `Promise.all` pattern.
//
// The wizard issuer-picker (`wizards/widgets/issuer_picker.dart`) has
// its own `issuerListProvider` keyed on `(clusterId, namespace)` that
// returns just plain-string name lists. The browse-all surface needs
// full [Issuer] records (type / ready / threshold annotations), so
// [allIssuersProvider] in the cert-manager repository fetches both
// endpoints in parallel and concatenates the results.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/certmanager_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'cert_badges.dart';

class IssuersListScreen extends ConsumerWidget {
  const IssuersListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(certManagerStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('Issuers')),
      body: statusAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ErrorStateView(
          message: e is ApiError ? e.message : e.toString(),
          onRetry: () =>
              ref.invalidate(certManagerStatusProvider(clusterId)),
        ),
        data: (status) {
          if (!status.detected) return FeatureUnavailableState.certManager();
          return _IssuersBody(clusterId: clusterId);
        },
      ),
    );
  }
}

class _IssuersBody extends ConsumerWidget {
  const _IssuersBody({required this.clusterId});

  final String clusterId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(allIssuersProvider(clusterId));

    Future<void> handleRefresh() async {
      ref.invalidate(allIssuersProvider(clusterId));
      try {
        await ref.read(allIssuersProvider(clusterId).future);
      } on Object {
        // surfaces via .when error branch
      }
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _errorShell(e, handleRefresh, colors),
        data: (issuers) {
          if (issuers.isEmpty) {
            return ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: [
                SizedBox(
                  height: 280,
                  child: Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Text(
                        'No Issuers or ClusterIssuers configured on this cluster.',
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
            itemCount: issuers.length,
            itemBuilder: (context, index) => _IssuerRow(issuer: issuers[index]),
          );
        },
      ),
    );
  }

  Widget _errorShell(Object e, Future<void> Function() retry, KubeColors c) {
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [
        SizedBox(
          height: 280,
          child: Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    'Failed to load issuers',
                    style: TextStyle(
                      color: c.textPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    e is ApiError ? e.message : e.toString(),
                    style: TextStyle(color: c.textMuted),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 12),
                  OutlinedButton(
                    onPressed: retry,
                    child: const Text('Retry'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class _IssuerRow extends StatelessWidget {
  const _IssuerRow({required this.issuer});

  final Issuer issuer;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final scopeLabel = issuer.isCluster ? 'ClusterIssuer' : 'Issuer';
    final namespacePart = issuer.isCluster ? '' : ' · ${issuer.namespace}';
    final detail = issuer.acmeServer ?? issuer.reason ?? '';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(color: colors.borderSubtle),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  issuer.name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 15,
                    fontWeight: FontWeight.w600,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              const SizedBox(width: 8),
              IssuerTypeBadge(type: issuer.type),
              const SizedBox(width: 6),
              _ReadyBadge(ready: issuer.ready),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            '$scopeLabel$namespacePart',
            style: TextStyle(color: colors.textSecondary, fontSize: 12),
          ),
          if (detail.isNotEmpty) ...[
            const SizedBox(height: 2),
            Text(
              detail,
              style: TextStyle(color: colors.textMuted, fontSize: 11),
              overflow: TextOverflow.ellipsis,
              maxLines: 2,
            ),
          ],
        ],
      ),
    );
  }
}

class _ReadyBadge extends StatelessWidget {
  const _ReadyBadge({required this.ready});

  final bool ready;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final tone = ready ? colors.success : colors.error;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: tone.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: tone.withValues(alpha: 0.4)),
      ),
      child: Text(
        ready ? 'Ready' : 'Not ready',
        style: TextStyle(
          color: tone,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
