// App-bar action affordance that drives the full write-action flow:
// open ActionSheet → route by ActionId (ScaleSheet for scale, ConfirmSheet
// for the rest) → execute → snackbar success/failure → invalidate the
// resource's resourceGetProvider so the detail screen refetches.
//
// Detail screens compose this into [ResourceDetailScaffold.trailingAction].
// Rollback is declared in [ActionId] for stability across PRs but its UI
// (revision picker) ships in PR-2b — selecting it here surfaces a
// "ships in PR-2b" snackbar rather than firing the POST.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';
import '../api/resource_actions.dart';
import '../api/resource_repository.dart';
import '../auth/auth_repository.dart';
import '../auth/auth_state.dart';
import '../cluster/cluster_provider.dart';
import '../features/resources/scale_sheet.dart';
import 'action_sheet.dart';
import 'confirm_sheet.dart';

class ResourceActionsButton extends ConsumerWidget {
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
  Widget build(BuildContext context, WidgetRef ref) {
    // Hide entirely when no actions are available for this kind. The
    // FAB-on-empty case is acceptable but cosmetic; suppressing the icon
    // avoids the dead-tap UX.
    if ((actionsByKind[kind] ?? const []).isEmpty) {
      return const SizedBox.shrink();
    }

    return IconButton(
      tooltip: 'Actions',
      icon: const Icon(Icons.bolt_outlined),
      onPressed: () => _onTap(context, ref),
    );
  }

  Future<void> _onTap(BuildContext context, WidgetRef ref) async {
    final auth = ref.read(authRepositoryProvider);
    final rbac = auth is AuthAuthenticated ? auth.rbac : null;
    if (!context.mounted) return;
    final id = await showActionSheet(
      context: context,
      kind: kind,
      namespace: namespace,
      resource: resource,
      rbac: rbac,
    );
    if (id == null || !context.mounted) return;

    switch (id) {
      case ActionId.scale:
        await _runScale(context, ref);
        return;
      case ActionId.rollback:
        // Rollback's revision picker lands in PR-2b. Telling the operator
        // is friendlier than firing a partial flow.
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Rollback ships in M2 PR-2b.')),
        );
        return;
      case ActionId.restart:
      case ActionId.delete:
      case ActionId.suspend:
      case ActionId.trigger:
        await _runConfirmThenAct(context, ref, id);
        return;
    }
  }

  Future<void> _runScale(BuildContext context, WidgetRef ref) async {
    final current =
        ((resource['spec'] as Map?)?['replicas'] as num?)?.toInt() ?? 0;
    final replicas = await showScaleSheet(
      context: context,
      name: name,
      currentReplicas: current,
    );
    if (replicas == null || !context.mounted) return;
    await _execute(
      context,
      ref,
      ActionId.scale,
      params: {'replicas': replicas},
    );
  }

  Future<void> _runConfirmThenAct(
    BuildContext context,
    WidgetRef ref,
    ActionId id,
  ) async {
    final meta = getActionMeta(id, resource);
    final ok = await showConfirmSheet(
      context: context,
      title: '${meta.label} $name',
      message: meta.confirmMessage,
      confirmLabel: meta.label,
      danger: meta.danger,
      typeToConfirm: meta.typeToConfirm,
    );
    if (ok != true || !context.mounted) return;

    Map<String, dynamic>? params;
    if (id == ActionId.suspend) {
      // Toggle current state. getActionMeta's label is the *next* action;
      // the request body needs the *target* boolean.
      final spec = resource['spec'] as Map<String, dynamic>? ?? const {};
      final currentlySuspended = spec['suspend'] == true;
      params = {'suspend': !currentlySuspended};
    }

    await _execute(context, ref, id, params: params);
  }

  Future<void> _execute(
    BuildContext context,
    WidgetRef ref,
    ActionId id, {
    Map<String, dynamic>? params,
  }) async {
    final messenger = ScaffoldMessenger.of(context);
    try {
      final result = await executeAction(
        dio: ref.read(dioProvider),
        id: id,
        kind: kind,
        namespace: namespace,
        name: name,
        params: params,
      );
      if (!context.mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(result.message)));

      // Invalidate the detail's GET so Overview/YAML/status refetch.
      // For delete, the screen typically pops; the invalidation is
      // harmless in either case.
      final clusterId = ref.read(activeClusterProvider);
      ref.invalidate(resourceGetProvider(ResourceGetKey(
        clusterId: clusterId,
        kind: kind,
        namespace: namespace,
        name: name,
      )));
      // List views (e.g., the workloads/pods table) auto-dispose, so a
      // hot list also picks up the change next render via its own
      // FutureProvider lifecycle. No global invalidation needed.

      if (id == ActionId.delete && context.mounted) {
        Navigator.of(context).maybePop();
      }
    } on ApiError catch (e) {
      if (!context.mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    } catch (e) {
      if (!context.mounted) return;
      messenger.showSnackBar(SnackBar(content: Text(e.toString())));
    }
  }
}
