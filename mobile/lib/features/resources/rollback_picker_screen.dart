// Rollback revision picker. Lists prior ReplicaSet revisions for a
// Deployment, plus a Confirm sheet to fire the rollback.
//
// Source of revisions: the Deployment exposes its history via owned
// ReplicaSets (each carries
// `metadata.annotations["deployment.kubernetes.io/revision"]`). We list
// `replicasets` in the deployment's namespace and filter to those whose
// `metadata.ownerReferences[0]` points at this Deployment. This matches
// the data `kubectl rollout history deployment` reads.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/dio_client.dart';
import '../../api/resource_actions.dart';
import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/confirm_sheet.dart';
import '../../widgets/empty_states.dart';
import 'k8s_helpers.dart';

class RollbackPickerScreen extends ConsumerWidget {
  const RollbackPickerScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: 'deployments',
      namespace: namespace,
      name: name,
    );
    final listKey = ResourceListKey(
      clusterId: clusterId,
      kind: 'replicasets',
      namespace: namespace,
    );
    final deployment = ref.watch(resourceGetProvider(getKey));
    final rsList = ref.watch(resourceListProvider(listKey));
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Rollback $name',
              style: TextStyle(color: colors.textPrimary, fontSize: 16),
              overflow: TextOverflow.ellipsis,
            ),
            Text(
              'Deployment · $namespace',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
      body: deployment.when(
        loading: () => const LoadingState(),
        error: (e, _) => ErrorStateView(
          message: e.toString(),
          onRetry: () => ref.invalidate(resourceGetProvider(getKey)),
        ),
        data: (deploymentRaw) {
          return rsList.when(
            loading: () => const LoadingState(),
            error: (e, _) => ErrorStateView(
              message: e.toString(),
              onRetry: () => ref.invalidate(resourceListProvider(listKey)),
            ),
            data: (rsResult) {
              final dMeta = K8sMeta.from(deploymentRaw);
              final revisions = _revisionsFor(rsResult.items, dMeta.uid);
              if (revisions.isEmpty) {
                return Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text(
                      'No prior revisions to roll back to. The deployment '
                      'may have only one revision in history (default '
                      'spec.revisionHistoryLimit is 10).',
                      style: TextStyle(color: colors.textSecondary),
                      textAlign: TextAlign.center,
                    ),
                  ),
                );
              }
              return RefreshIndicator(
                onRefresh: () async {
                  ref.invalidate(resourceListProvider(listKey));
                  await ref.read(resourceListProvider(listKey).future);
                },
                child: ListView.separated(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  itemCount: revisions.length,
                  separatorBuilder: (_, _) => Divider(
                    color: colors.borderSubtle,
                    height: 1,
                  ),
                  itemBuilder: (context, i) {
                    final rev = revisions[i];
                    return _RevisionTile(
                      revision: rev,
                      onTap: () => _confirmRollback(context, ref, rev),
                    );
                  },
                ),
              );
            },
          );
        },
      ),
    );
  }

  /// Filter ReplicaSets owned by this Deployment, parse their revision
  /// annotation, sort newest revision first.
  List<_RevisionEntry> _revisionsFor(
    List<Map<String, dynamic>> rsItems,
    String deploymentUid,
  ) {
    final out = <_RevisionEntry>[];
    for (final rs in rsItems) {
      final meta = rs['metadata'] as Map<String, dynamic>? ?? const {};
      final owners =
          (meta['ownerReferences'] as List?) ?? const <dynamic>[];
      final ownsByThis = owners.any((o) =>
          o is Map &&
          (o['kind'] == 'Deployment') &&
          (o['uid'] == deploymentUid));
      if (!ownsByThis) continue;
      final annotations =
          meta['annotations'] as Map<String, dynamic>? ?? const {};
      final revStr =
          annotations['deployment.kubernetes.io/revision'] as String? ?? '';
      final rev = int.tryParse(revStr);
      if (rev == null) continue;
      out.add(_RevisionEntry(
        revision: rev,
        creationTimestamp:
            (meta['creationTimestamp'] as String?) ?? '',
        changeCause: annotations['kubernetes.io/change-cause'] as String?,
      ));
    }
    out.sort((a, b) => b.revision.compareTo(a.revision));
    return out;
  }

  Future<void> _confirmRollback(
    BuildContext context,
    WidgetRef ref,
    _RevisionEntry rev,
  ) async {
    final messenger = ScaffoldMessenger.of(context);
    final ok = await showConfirmSheet(
      context: context,
      title: 'Rollback $name',
      message: rev.changeCause != null
          ? 'Roll back to revision ${rev.revision}\n${rev.changeCause}'
          : 'Roll back to revision ${rev.revision}',
      confirmLabel: 'Rollback',
      danger: true,
    );
    if (ok != true || !context.mounted) return;

    try {
      final result = await executeAction(
        dio: ref.read(dioProvider),
        id: ActionId.rollback,
        kind: 'deployments',
        namespace: namespace,
        name: name,
        params: {'revision': rev.revision},
      );
      if (!context.mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(result.message)));

      final clusterId = ref.read(activeClusterProvider);
      ref.invalidate(resourceGetProvider(ResourceGetKey(
        clusterId: clusterId,
        kind: 'deployments',
        namespace: namespace,
        name: name,
      )));
      ref.invalidate(resourceListProvider);
      Navigator.of(context).maybePop();
    } on ApiError catch (e) {
      if (!context.mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    } catch (_) {
      if (!context.mounted) return;
      messenger.showSnackBar(
        const SnackBar(
          content: Text(
            'Rollback failed unexpectedly. Check the backend logs.',
          ),
        ),
      );
    }
  }
}

class _RevisionEntry {
  const _RevisionEntry({
    required this.revision,
    required this.creationTimestamp,
    this.changeCause,
  });

  final int revision;
  final String creationTimestamp;
  final String? changeCause;
}

class _RevisionTile extends StatelessWidget {
  const _RevisionTile({required this.revision, required this.onTap});

  final _RevisionEntry revision;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ListTile(
      leading: CircleAvatar(
        backgroundColor: colors.accent.withValues(alpha: 0.16),
        child: Text(
          '${revision.revision}',
          style: TextStyle(color: colors.accent, fontWeight: FontWeight.w600),
        ),
      ),
      title: Text(
        'Revision ${revision.revision}',
        style: TextStyle(color: colors.textPrimary),
      ),
      subtitle: Text(
        revision.changeCause != null
            ? '${revision.changeCause}  ·  ${formatAge(revision.creationTimestamp)} ago'
            : '${formatAge(revision.creationTimestamp)} ago',
        style: TextStyle(color: colors.textSecondary, fontSize: 12),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: Icon(Icons.chevron_right, color: colors.textMuted),
      onTap: onTap,
    );
  }
}
