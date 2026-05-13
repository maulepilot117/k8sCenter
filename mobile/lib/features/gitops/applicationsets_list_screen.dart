// Argo CD ApplicationSets list. Argo-only — gated first on
// `status.isInstalled` (GitOps not detected at all) then on
// `status.argoCD.appSetsAvailable` (Argo present but AppSet CRD
// missing). When the CRD is not installed the screen renders
// `FeatureUnavailableState.gitops()` with a hint about the AppSet CRD.
//
// Tap a row → composite-ID detail route. AppSets share the same
// `tool:ns:name` shape as Applications, with the `argo-as` tool
// prefix.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/gitops_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'gitops_widgets.dart';

class ApplicationSetsListScreen extends ConsumerWidget {
  const ApplicationSetsListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(gitOpsStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('ApplicationSets')),
      body: statusAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: Text(e.toString())),
        data: (status) {
          if (!status.isInstalled) {
            return FeatureUnavailableState.gitops();
          }
          if (!status.argoCD.appSetsAvailable) {
            return const _AppSetsUnavailable();
          }
          return _AppSetsBody(clusterId: clusterId);
        },
      ),
    );
  }
}

class _AppSetsUnavailable extends StatelessWidget {
  const _AppSetsUnavailable();

  @override
  Widget build(BuildContext context) {
    return const FeatureUnavailableState(
      featureName: 'Argo CD ApplicationSets',
      helpMessage:
          'The ApplicationSet CRD is not installed on this cluster. '
          'Install argo-cd with the applicationset-controller enabled to '
          'manage Application generators.',
      icon: Icons.account_tree_outlined,
    );
  }
}

class _AppSetsBody extends ConsumerWidget {
  const _AppSetsBody({required this.clusterId});

  final String clusterId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(gitOpsApplicationSetsProvider(clusterId));

    Future<void> handleRefresh() async {
      ref.invalidate(gitOpsApplicationSetsProvider(clusterId));
      try {
        await ref.read(gitOpsApplicationSetsProvider(clusterId).future);
      } on Object {
        // surfaces via error branch
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
              child: Center(
                child: Padding(
                  padding: const EdgeInsets.all(24),
                  child: Text(
                    e.toString(),
                    style: TextStyle(color: colors.textMuted),
                    textAlign: TextAlign.center,
                  ),
                ),
              ),
            ),
          ],
        ),
        data: (sets) {
          if (sets.isEmpty) {
            return ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: [
                SizedBox(
                  height: 280,
                  child: Center(
                    child: Text(
                      'No ApplicationSets in this cluster.',
                      style: TextStyle(color: colors.textMuted),
                    ),
                  ),
                ),
              ],
            );
          }
          return ListView.separated(
            physics: const AlwaysScrollableScrollPhysics(),
            itemCount: sets.length,
            separatorBuilder: (_, _) => Divider(
              color: colors.borderSubtle,
              height: 1,
              indent: 16,
              endIndent: 16,
            ),
            itemBuilder: (context, i) {
              final set = sets[i];
              return InkWell(
                onTap: () => context.push(
                  '/clusters/$clusterId/gitops/applicationsets/'
                  '${Uri.encodeComponent(set.id)}',
                ),
                child: Padding(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 16, vertical: 12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        set.name,
                        style: TextStyle(
                          color: colors.textPrimary,
                          fontWeight: FontWeight.w600,
                          fontSize: 15,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        '${set.namespace}  ·  ${set.generatorTypes.isEmpty ? "no generators" : set.generatorTypes.join(", ")}',
                        style:
                            TextStyle(color: colors.textMuted, fontSize: 12),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      const SizedBox(height: 6),
                      Row(
                        children: [
                          StatusPill(
                            label: set.status.isEmpty
                                ? 'unknown'
                                : set.status,
                            color: statusColor(colors, set.status),
                          ),
                          const SizedBox(width: 8),
                          Text(
                            '${set.generatedAppCount} apps',
                            style: TextStyle(
                                color: colors.textSecondary, fontSize: 12),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              );
            },
          );
        },
      ),
    );
  }
}

