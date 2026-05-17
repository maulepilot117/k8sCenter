// Expiring certificates surface — fetches `/v1/certificates/expiring`
// and renders the ordered list (sorted ascending by daysRemaining by
// the backend). Distinct from the certificates list with a
// `?status=expiring` chip pre-filter because:
//   * The /expiring endpoint emits a smaller summary record per cert
//     (no DNS names, no thresholds, no sub-resource hooks).
//   * Severity is precomputed by the backend (`"critical"` /
//     `"warning"` / `"expired"`) so the screen never has to recompute
//     the threshold buckets here.
//   * The screen is the natural landing surface for the expiry
//     notification deep link.
//
// Tap row → certificate detail (same path as the cert list).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/certmanager_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/feature_unavailable_state.dart';

class ExpiringCertificatesScreen extends ConsumerWidget {
  const ExpiringCertificatesScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(certManagerStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('Expiring certificates')),
      body: statusAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ErrorStateView(
          message: e is ApiError ? e.message : e.toString(),
          onRetry: () =>
              ref.invalidate(certManagerStatusProvider(clusterId)),
        ),
        data: (status) {
          if (!status.detected) return FeatureUnavailableState.certManager();
          return _ExpiringBody(clusterId: clusterId);
        },
      ),
    );
  }
}

class _ExpiringBody extends ConsumerWidget {
  const _ExpiringBody({required this.clusterId});

  final String clusterId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(expiringCertificatesProvider(clusterId));

    Future<void> handleRefresh() async {
      ref.invalidate(expiringCertificatesProvider(clusterId));
      try {
        await ref.read(expiringCertificatesProvider(clusterId).future);
      } on Object {
        // surfaces via .when error branch
      }
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: [
            SizedBox(
              height: 280,
              child: ErrorStateView(
                message: e is ApiError ? e.message : e.toString(),
                onRetry: handleRefresh,
              ),
            ),
          ],
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
                      padding: const EdgeInsets.all(24),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          ExcludeSemantics(
                            child: Icon(
                              Icons.verified_outlined,
                              color: colors.success,
                              size: 36,
                            ),
                          ),
                          const SizedBox(height: 12),
                          Text(
                            'No certificates expiring soon.',
                            style: TextStyle(color: colors.textSecondary),
                            textAlign: TextAlign.center,
                          ),
                        ],
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
            itemBuilder: (context, index) =>
                _ExpiringRow(item: items[index], clusterId: clusterId),
          );
        },
      ),
    );
  }
}

class _ExpiringRow extends StatelessWidget {
  const _ExpiringRow({required this.item, required this.clusterId});

  final ExpiringCertificate item;
  final String clusterId;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return InkWell(
      onTap: () => context.push(
        '/clusters/$clusterId/certificates/certificates/'
        '${Uri.encodeComponent(item.namespace)}/'
        '${Uri.encodeComponent(item.name)}',
      ),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        decoration: BoxDecoration(
          border: Border(
            bottom: BorderSide(color: colors.borderSubtle),
          ),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              padding: const EdgeInsets.symmetric(
                horizontal: 8,
                vertical: 4,
              ),
              decoration: BoxDecoration(
                color: _severityColor(colors, item.severity)
                    .withValues(alpha: 0.16),
                borderRadius: BorderRadius.circular(8),
                border: Border.all(
                  color: _severityColor(colors, item.severity)
                      .withValues(alpha: 0.4),
                ),
              ),
              child: Text(
                item.daysRemaining < 0 ? 'Expired' : '${item.daysRemaining}d',
                style: TextStyle(
                  color: _severityColor(colors, item.severity),
                  fontSize: 13,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    item.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 15,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  Text(
                    '${item.namespace} · ${item.issuerName}',
                    style:
                        TextStyle(color: colors.textSecondary, fontSize: 12),
                    overflow: TextOverflow.ellipsis,
                  ),
                  Text(
                    'Expires ${item.notAfter}',
                    style: TextStyle(color: colors.textMuted, fontSize: 11),
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            ExcludeSemantics(
              child: Icon(Icons.chevron_right, size: 16, color: colors.textMuted),
            ),
          ],
        ),
      ),
    );
  }

  Color _severityColor(KubeColors c, String severity) {
    return switch (severity) {
      'critical' => c.error,
      'expired' => c.error,
      _ => c.warning,
    };
  }
}
