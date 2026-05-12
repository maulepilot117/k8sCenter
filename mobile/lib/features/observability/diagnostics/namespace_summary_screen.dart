// "Failing Pods in <namespace>" — namespace-level health summary tile.
// Backend's `/diagnostics/{ns}/summary` endpoint is pod-only today
// (`backend/internal/diagnostics/handler.go::HandleNamespaceSummary`
// only iterates `pods`), so the surface labels itself pod-specifically
// rather than the broader "failing resources". A future backend
// extension that returns Deployments / PVCs etc. will need the title
// to soften — keep the wording change scoped here.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../api/api_error.dart';
import '../../../api/diagnostics_repository.dart';
import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../../widgets/empty_states.dart';
import 'diagnostics_controller.dart';
import 'scrollable_center.dart';

class NamespaceSummaryScreen extends ConsumerWidget {
  const NamespaceSummaryScreen({super.key, required this.namespace});

  final String namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final key = (clusterId: clusterId, namespace: namespace);
    final summary = ref.watch(namespaceSummaryProvider(key));
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              'Failing Pods',
              style: TextStyle(color: colors.textPrimary, fontSize: 16),
            ),
            Text(
              namespace,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
      body: RefreshIndicator(
        onRefresh: () async => ref.invalidate(namespaceSummaryProvider(key)),
        child: summary.when(
          loading: () => const ScrollableCenter(child: LoadingState()),
          error: (e, _) => ScrollableCenter(
            child: ErrorStateView(
              message: e is ApiError ? e.message : e.toString(),
              onRetry: () => ref.invalidate(namespaceSummaryProvider(key)),
            ),
          ),
          data: (data) => _NamespaceSummaryBody(
            clusterId: clusterId,
            namespace: namespace,
            summary: data,
          ),
        ),
      ),
    );
  }
}

class _NamespaceSummaryBody extends StatelessWidget {
  const _NamespaceSummaryBody({
    required this.clusterId,
    required this.namespace,
    required this.summary,
  });

  final String clusterId;
  final String namespace;
  final NamespaceSummary summary;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    if (summary.isHealthy) {
      return ScrollableCenter(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.check_circle_outline, color: colors.success, size: 48),
            const SizedBox(height: 12),
            Text(
              summary.total == 0
                  ? 'No pods running in this namespace'
                  : 'All ${summary.total} pods are healthy',
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 16,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              namespace,
              style: TextStyle(color: colors.textMuted, fontSize: 13),
            ),
          ],
        ),
      );
    }
    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: summary.failing.length + 1,
      separatorBuilder: (_, _) =>
          Divider(height: 1, color: colors.borderSubtle),
      itemBuilder: (context, i) {
        if (i == 0) {
          return Padding(
            padding:
                const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            child: Text(
              '${summary.failing.length} of ${summary.total} pods failing',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          );
        }
        final row = summary.failing[i - 1];
        return ListTile(
          dense: true,
          leading: Icon(Icons.error_outline, color: colors.error, size: 20),
          title: Text(
            row.name,
            style: TextStyle(
              color: colors.textPrimary,
              fontWeight: FontWeight.w500,
            ),
          ),
          subtitle: Text(
            row.reason,
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
          trailing: Icon(Icons.chevron_right, color: colors.textMuted),
          // Drill from "what's failing here?" into "why is this specific
          // pod failing?" — opens the per-pod diagnostics screen.
          onTap: () => context.push(
            '/clusters/$clusterId/diagnostics/$namespace/Pod/${row.name}',
          ),
        );
      },
    );
  }
}
