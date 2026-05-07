// App-bar action affordance that drives the full write-action flow:
// open ActionSheet → route by ActionId (ScaleSheet for scale, ConfirmSheet
// for the rest) → execute → snackbar success/failure → invalidate the
// resource's resourceGetProvider so the detail screen refetches.
//
// Detail screens compose this into [ResourceDetailScaffold.trailingAction].
//
// Safety invariants:
//   1. Cluster context is pinned at sheet open. If the operator switches
//      clusters mid-flow (via the cluster pill), the action aborts at
//      execute time with an explanatory snackbar — without this, the
//      type-to-confirm gate is meaningless across cluster boundaries
//      (a confirmed Delete on cluster A could hit a same-named resource
//      on cluster B because ClusterInterceptor reads the active cluster
//      at request-send time, not at sheet-open).
//   2. The button is disabled while an action is in flight. A double-tap
//      cannot fire two parallel destructive POSTs.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';
import '../api/resource_actions.dart';
import '../api/resource_repository.dart';
import '../auth/auth_repository.dart';
import '../auth/auth_state.dart';
import '../cluster/cluster_provider.dart';
import 'action_sheet.dart';
import 'confirm_sheet.dart';
import 'scale_sheet.dart';

class ResourceActionsButton extends ConsumerStatefulWidget {
  const ResourceActionsButton({
    super.key,
    required this.kind,
    required this.namespace,
    required this.name,
    required this.resource,
  });

  /// Canonical resource kind ('deployments', 'pods', etc.) — must match
  /// `actionsByKind` keys.
  final String kind;
  final String namespace;
  final String name;
  final Map<String, dynamic> resource;

  @override
  ConsumerState<ResourceActionsButton> createState() =>
      _ResourceActionsButtonState();
}

class _ResourceActionsButtonState extends ConsumerState<ResourceActionsButton> {
  bool _executing = false;

  @override
  Widget build(BuildContext context) {
    if ((actionsByKind[widget.kind] ?? const []).isEmpty) {
      return const SizedBox.shrink();
    }
    return IconButton(
      tooltip: 'Actions',
      icon: const Icon(Icons.bolt_outlined),
      onPressed: _executing ? null : _onTap,
    );
  }

  Future<void> _onTap() async {
    if (_executing) return;
    setState(() => _executing = true);
    try {
      await _runFlow();
    } finally {
      if (mounted) setState(() => _executing = false);
    }
  }

  Future<void> _runFlow() async {
    // Pin the cluster at sheet-open. If the operator switches clusters
    // before confirming, _execute aborts rather than firing the write
    // against the new cluster.
    final pinnedCluster = ref.read(activeClusterProvider);
    final auth = ref.read(authRepositoryProvider);
    final rbac = auth is AuthAuthenticated ? auth.rbac : null;
    if (!mounted) return;

    final id = await showActionSheet(
      context: context,
      kind: widget.kind,
      namespace: widget.namespace,
      resource: widget.resource,
      rbac: rbac,
    );
    if (id == null || !mounted) return;

    switch (id) {
      case ActionId.scale:
        await _runScale(pinnedCluster);
        return;
      case ActionId.rollback:
        // Rollback ships in PR-2b. Filtered out of `actionsByKind` so this
        // case is unreachable; kept for enum exhaustiveness.
        return;
      case ActionId.restart:
      case ActionId.delete:
      case ActionId.suspend:
      case ActionId.trigger:
        await _runConfirmThenAct(id, pinnedCluster);
        return;
    }
  }

  Future<void> _runScale(String pinnedCluster) async {
    final current =
        ((widget.resource['spec'] as Map?)?['replicas'] as num?)?.toInt() ?? 0;
    final replicas = await showScaleSheet(
      context: context,
      name: widget.name,
      currentReplicas: current,
    );
    if (replicas == null || !mounted) return;
    await _execute(
      ActionId.scale,
      pinnedCluster,
      params: {'replicas': replicas},
    );
  }

  Future<void> _runConfirmThenAct(ActionId id, String pinnedCluster) async {
    final meta = getActionMeta(id, widget.resource);
    final ok = await showConfirmSheet(
      context: context,
      title: '${meta.label} ${widget.name}',
      message: meta.confirmMessage,
      confirmLabel: meta.label,
      danger: meta.danger,
      typeToConfirm: meta.typeToConfirm,
    );
    if (ok != true || !mounted) return;

    Map<String, dynamic>? params;
    if (id == ActionId.suspend) {
      // Toggle from the snapshot the operator confirmed against. A small
      // race exists if another oncall mutated the resource between detail
      // load and this confirm — backend is idempotent (suspend twice is a
      // no-op; resume twice is a no-op) so the worst case is a wasted PUT,
      // not state corruption. Surfacing a confirmation against fresh state
      // would add a network round-trip per suspend tap; not worth it.
      final spec =
          widget.resource['spec'] as Map<String, dynamic>? ?? const {};
      final currentlySuspended = spec['suspend'] == true;
      params = {'suspend': !currentlySuspended};
    }

    await _execute(id, pinnedCluster, params: params);
  }

  Future<void> _execute(
    ActionId id,
    String pinnedCluster, {
    Map<String, dynamic>? params,
  }) async {
    final messenger = ScaffoldMessenger.of(context);

    // Cluster-drift check: if the operator switched clusters between
    // sheet-open and now, abort. The type-to-confirm name only identifies
    // the resource within the cluster it was viewed from.
    final activeCluster = ref.read(activeClusterProvider);
    if (activeCluster != pinnedCluster) {
      messenger.showSnackBar(
        const SnackBar(
          content: Text(
            'Cluster changed during this action. Aborted to avoid '
            'mutating the wrong cluster. Re-run the action.',
          ),
        ),
      );
      return;
    }

    try {
      final result = await executeAction(
        dio: ref.read(dioProvider),
        id: id,
        kind: widget.kind,
        namespace: widget.namespace,
        name: widget.name,
        params: params,
      );
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(result.message)));

      // Invalidate the detail's GET so Overview/YAML/status refetch.
      ref.invalidate(resourceGetProvider(ResourceGetKey(
        clusterId: pinnedCluster,
        kind: widget.kind,
        namespace: widget.namespace,
        name: widget.name,
      )));
      // Also invalidate the family of list providers — the list screen
      // upstream of this detail keeps its listener under the nav stack and
      // doesn't auto-refetch otherwise. Family-wide invalidation is broad
      // but cheap; the autoDispose families re-fetch only when listened.
      ref.invalidate(resourceListProvider);

      if (id == ActionId.delete && mounted) {
        Navigator.of(context).maybePop();
      }
    } on ApiError catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    } catch (_) {
      // Don't leak raw exception toString() to the operator — it can carry
      // type names, partial stack info, or unexpected payload structure.
      if (!mounted) return;
      messenger.showSnackBar(
        const SnackBar(
          content: Text(
            'Action failed unexpectedly. Check the backend logs.',
          ),
        ),
      );
    }
  }
}
