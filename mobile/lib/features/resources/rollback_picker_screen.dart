// Rollback revision picker. Lists prior ReplicaSet revisions for a
// Deployment, plus a Confirm sheet to fire the rollback.
//
// Source of revisions: the Deployment exposes its history via owned
// ReplicaSets (each carries
// `metadata.annotations["deployment.kubernetes.io/revision"]`). We list
// `replicasets` in the deployment's namespace and filter to those whose
// `metadata.ownerReferences[0]` points at this Deployment. This matches
// the data `kubectl rollout history deployment` reads.
//
// Cluster pinning: the picker captures the active cluster in the route
// pushed by ResourceActionsButton (clusterId is in the URL). Inside the
// screen we still re-check `activeClusterProvider` against the pinned
// id at confirm time so a mid-flow cluster switch aborts the rollback
// rather than firing it at the wrong cluster — the same defense
// pattern as ResourceActionsButton itself.

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

/// Annotation key Kubernetes attaches to each ReplicaSet owned by a
/// Deployment. Used both at runtime (`_revisionsFor`) and in tests, so
/// the literal lives at exactly one location.
const String kRevisionAnnotation = 'deployment.kubernetes.io/revision';

class RollbackPickerScreen extends ConsumerStatefulWidget {
  const RollbackPickerScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  ConsumerState<RollbackPickerScreen> createState() =>
      _RollbackPickerScreenState();
}

class _RollbackPickerScreenState extends ConsumerState<RollbackPickerScreen> {
  /// Cluster captured at first build. Compared against the active
  /// cluster at confirm time to detect mid-flow switches.
  late final String _pinnedCluster = ref.read(activeClusterProvider);

  /// Single in-flight rollback guard — prevents double-tap from stacking
  /// two confirm sheets / two POSTs.
  bool _confirming = false;

  @override
  Widget build(BuildContext context) {
    final getKey = ResourceGetKey(
      clusterId: _pinnedCluster,
      kind: 'deployments',
      namespace: widget.namespace,
      name: widget.name,
    );
    final listKey = ResourceListKey(
      clusterId: _pinnedCluster,
      kind: 'replicasets',
      namespace: widget.namespace,
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
              'Rollback ${widget.name}',
              style: TextStyle(color: colors.textPrimary, fontSize: 16),
              overflow: TextOverflow.ellipsis,
            ),
            Text(
              'Deployment · ${widget.namespace}',
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
              final currentRevision = _currentRevision(deploymentRaw);
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
                  // Pull-to-refresh must not bubble exceptions: Flutter's
                  // RefreshIndicator silently stalls if onRefresh throws,
                  // and the ErrorStateView below only catches the initial
                  // load. Catch and let the next .when() error path
                  // render the failure surface.
                  try {
                    await ref.read(resourceListProvider(listKey).future);
                  } catch (_) {
                    /* surfaced via rsList.when error branch */
                  }
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
                    final isCurrent = rev.revision == currentRevision;
                    return _RevisionTile(
                      revision: rev,
                      isCurrent: isCurrent,
                      onTap: isCurrent
                          ? null
                          : () => _confirmRollback(context, rev, listKey),
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

  /// Reads the deployment's currently-rolled-out revision from the same
  /// annotation the picker filters ReplicaSets on. The current revision
  /// is rendered with a "Current" pill and tap-disabled so the operator
  /// can't roll back to the version they're already on.
  int? _currentRevision(Map<String, dynamic> deployment) {
    final meta = deployment['metadata'] as Map<String, dynamic>? ?? const {};
    final ann = meta['annotations'] as Map<String, dynamic>? ?? const {};
    final raw = ann[kRevisionAnnotation] as String? ?? '';
    return int.tryParse(raw);
  }

  /// Filter ReplicaSets owned by this Deployment, parse their revision
  /// annotation, sort newest revision first, and de-duplicate (k8s can
  /// briefly carry two RS at the same revision during pause/resume).
  List<_RevisionEntry> _revisionsFor(
    List<Map<String, dynamic>> rsItems,
    String deploymentUid,
  ) {
    final byRevision = <int, _RevisionEntry>{};
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
          annotations[kRevisionAnnotation] as String? ?? '';
      final rev = int.tryParse(revStr);
      if (rev == null) continue;
      // De-dupe: keep the newest creationTimestamp for any given
      // revision. Two RS at the same revision is rare but possible.
      final ts = (meta['creationTimestamp'] as String?) ?? '';
      final existing = byRevision[rev];
      if (existing == null || ts.compareTo(existing.creationTimestamp) > 0) {
        byRevision[rev] = _RevisionEntry(
          revision: rev,
          creationTimestamp: ts,
          changeCause:
              annotations['kubernetes.io/change-cause'] as String?,
        );
      }
    }
    final out = byRevision.values.toList()
      ..sort((a, b) => b.revision.compareTo(a.revision));
    return out;
  }

  Future<void> _confirmRollback(
    BuildContext context,
    _RevisionEntry rev,
    ResourceListKey listKey,
  ) async {
    if (_confirming) return;
    setState(() => _confirming = true);
    try {
      await _doConfirm(context, rev, listKey);
    } finally {
      if (mounted) setState(() => _confirming = false);
    }
  }

  Future<void> _doConfirm(
    BuildContext context,
    _RevisionEntry rev,
    ResourceListKey listKey,
  ) async {
    final messenger = ScaffoldMessenger.of(context);

    // Cluster-drift check — if the operator switched clusters between
    // picker open and now, abort. Same defense as ResourceActionsButton.
    final activeCluster = ref.read(activeClusterProvider);
    if (activeCluster != _pinnedCluster) {
      messenger.showSnackBar(
        const SnackBar(
          content: Text(
            'Cluster changed during this rollback. Aborted to avoid '
            'mutating the wrong cluster. Re-open the picker.',
          ),
        ),
      );
      return;
    }

    final ok = await showConfirmSheet(
      context: context,
      title: 'Rollback ${widget.name}',
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
        namespace: widget.namespace,
        name: widget.name,
        params: {'revision': rev.revision},
      );
      if (!context.mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(result.message)));

      // Scoped invalidations only — invalidating the entire
      // resourceListProvider family triggers refetch storms across all
      // cached lists (every namespace, every kind). Limit blast radius
      // to the deployment GET + this picker's RS list.
      ref.invalidate(resourceGetProvider(ResourceGetKey(
        clusterId: _pinnedCluster,
        kind: 'deployments',
        namespace: widget.namespace,
        name: widget.name,
      )));
      ref.invalidate(resourceListProvider(listKey));
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
  const _RevisionTile({
    required this.revision,
    required this.isCurrent,
    this.onTap,
  });

  final _RevisionEntry revision;
  final bool isCurrent;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final age = formatAge(revision.creationTimestamp);
    final semanticsLabel = isCurrent
        ? 'Revision ${revision.revision}, current, deployed $age ago'
        : revision.changeCause != null
            ? 'Roll back to revision ${revision.revision}, ${revision.changeCause}, deployed $age ago'
            : 'Roll back to revision ${revision.revision}, deployed $age ago';
    return Semantics(
      button: !isCurrent,
      enabled: !isCurrent,
      label: semanticsLabel,
      excludeSemantics: true,
      child: ListTile(
        enabled: !isCurrent,
        leading: ExcludeSemantics(
          child: CircleAvatar(
            backgroundColor: colors.accent.withValues(alpha: 0.16),
            child: Text(
              '${revision.revision}',
              style:
                  TextStyle(color: colors.accent, fontWeight: FontWeight.w600),
            ),
          ),
        ),
        title: Row(
          children: [
            Text(
              'Revision ${revision.revision}',
              style: TextStyle(color: colors.textPrimary),
            ),
            if (isCurrent) ...[
              const SizedBox(width: 8),
              // Feature-local "Current" pill — labeled so screen readers
              // announce it as part of the merged tile semantics above.
              ExcludeSemantics(
                child: Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: colors.accent.withValues(alpha: 0.16),
                    borderRadius: BorderRadius.circular(10),
                  ),
                  child: Text(
                    'Current',
                    style: TextStyle(
                      color: colors.accent,
                      fontSize: 11,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ),
            ],
          ],
        ),
        subtitle: Text(
          revision.changeCause != null
              ? '${revision.changeCause}  ·  $age ago'
              : '$age ago',
          style: TextStyle(color: colors.textSecondary, fontSize: 12),
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
        ),
        trailing: isCurrent
            ? null
            : ExcludeSemantics(
                child: Icon(Icons.chevron_right, color: colors.textMuted),
              ),
        onTap: onTap,
      ),
    );
  }
}
