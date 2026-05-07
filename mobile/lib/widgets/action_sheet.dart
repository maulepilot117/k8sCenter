// Per-resource action menu. Bottom sheet listing the actions
// `getVisibleActions` returns for the resource's kind+namespace under
// the current user's RBAC summary.
//
// Returns the chosen [ActionId] (or null on dismiss). Caller drives the
// follow-on flow — opens ScaleSheet for scale, ConfirmSheet for the rest.

import 'package:flutter/material.dart';

import '../api/resource_actions.dart';
import '../auth/user.dart';
import '../theme/kube_theme_builder.dart';

/// Open the action sheet for [resource]. Filters [actions] through the
/// user's RBAC summary; an empty result renders an empty-state explaining
/// no actions are available.
Future<ActionId?> showActionSheet({
  required BuildContext context,
  required String kind,
  required String namespace,
  required Map<String, dynamic> resource,
  required RBACSummary? rbac,
}) {
  return showModalBottomSheet<ActionId>(
    context: context,
    builder: (ctx) => ActionSheet(
      kind: kind,
      namespace: namespace,
      resource: resource,
      rbac: rbac,
    ),
  );
}

class ActionSheet extends StatelessWidget {
  const ActionSheet({
    super.key,
    required this.kind,
    required this.namespace,
    required this.resource,
    required this.rbac,
  });

  final String kind;
  final String namespace;
  final Map<String, dynamic> resource;
  final RBACSummary? rbac;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final visible = getVisibleActions(kind, namespace, rbac);

    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // Drag handle.
            Container(
              width: 36,
              height: 4,
              margin: const EdgeInsets.only(top: 4, bottom: 8),
              decoration: BoxDecoration(
                color: colors.borderSubtle,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 8),
              child: Row(
                children: [
                  Text(
                    'Actions',
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      letterSpacing: 0.5,
                    ),
                  ),
                ],
              ),
            ),
            if (visible.isEmpty)
              Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  'No actions available for this resource',
                  style: TextStyle(color: colors.textMuted, fontSize: 13),
                ),
              )
            else
              for (final id in visible)
                _ActionTile(id: id, resource: resource),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }
}

class _ActionTile extends StatelessWidget {
  const _ActionTile({required this.id, required this.resource});

  final ActionId id;
  final Map<String, dynamic> resource;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final meta = getActionMeta(id, resource);
    final tone = meta.danger ? colors.error : colors.textPrimary;
    return ListTile(
      leading: Icon(_iconFor(id), color: tone),
      title: Text(
        meta.label,
        style: TextStyle(
          color: tone,
          fontSize: 15,
          fontWeight: FontWeight.w500,
        ),
      ),
      onTap: () => Navigator.of(context).pop(id),
    );
  }

  IconData _iconFor(ActionId id) {
    switch (id) {
      case ActionId.scale:
        return Icons.tune;
      case ActionId.restart:
        return Icons.refresh;
      case ActionId.suspend:
        return Icons.pause_circle_outline;
      case ActionId.trigger:
        return Icons.play_circle_outline;
      case ActionId.rollback:
        return Icons.history;
      case ActionId.delete:
        return Icons.delete_outline;
    }
  }
}
