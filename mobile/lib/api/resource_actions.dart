// Resource action handlers — Dart port of `frontend/lib/action-handlers.ts`.
//
// Kept structurally isomorphic to the TS source so action drift between web
// and mobile is easy to spot in a diff. When the TS file gains a new action,
// kind, or verb, this file is the parallel edit.
//
// Action verbs hit `POST /api/v1/resources/:kind/:ns/:name/<verb>` (or
// `DELETE` for delete) — see `backend/internal/server/routes.go:758-762`.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../auth/permissions.dart';
import '../auth/user.dart';
import 'api_error.dart';
import 'dio_client.dart';

/// Valid action identifiers. Mirrors `ActionId` in action-handlers.ts.
enum ActionId { scale, restart, delete, suspend, trigger, rollback }

/// Actions available per resource kind. Source of truth lives in
/// `frontend/lib/action-handlers.ts:ACTIONS_BY_KIND` — this map is its
/// Dart twin and must stay in sync. Rollback is M2 PR-2b; declared here
/// (deployments only) so the enum + maps don't churn between PRs.
const Map<String, List<ActionId>> actionsByKind = {
  'deployments': [
    ActionId.scale,
    ActionId.restart,
    ActionId.rollback,
    ActionId.delete,
  ],
  'statefulsets': [ActionId.scale, ActionId.restart, ActionId.delete],
  'daemonsets': [ActionId.restart, ActionId.delete],
  'pods': [ActionId.delete],
  'jobs': [ActionId.suspend, ActionId.delete],
  'cronjobs': [ActionId.suspend, ActionId.trigger, ActionId.delete],
  'rolebindings': [ActionId.delete],
  'clusterrolebindings': [ActionId.delete],
};

/// Maps action IDs to the k8s verb required to perform them. Used by
/// [getVisibleActions] for client-side RBAC filtering. Trigger creates a
/// Job from a CronJob template, so the verb is "create".
const Map<ActionId, String> actionVerbMap = {
  ActionId.scale: 'update',
  ActionId.restart: 'update',
  ActionId.delete: 'delete',
  ActionId.suspend: 'update',
  ActionId.rollback: 'update',
  ActionId.trigger: 'create',
};

/// Filter actions to only those the user has k8s permission for.
/// Mirrors `getVisibleActions` in action-handlers.ts. When [rbac] is null,
/// returns all actions for the kind (optimistic — backend is final authority).
List<ActionId> getVisibleActions(
  String kind,
  String namespace,
  RBACSummary? rbac,
) {
  final all = actionsByKind[kind] ?? const <ActionId>[];
  if (rbac == null) return all;
  return all
      .where((id) => canPerform(rbac, kind, actionVerbMap[id]!, namespace))
      .toList();
}

/// Display metadata for an action.
class ActionMeta {
  const ActionMeta({
    required this.label,
    this.danger = false,
    this.confirm,
    this.confirmMessage,
    this.typeToConfirm,
  });

  final String label;
  final bool danger;

  /// `'confirm'` = simple OK/Cancel. `'destructive'` = require typing the
  /// resource name to enable the confirm button.
  final String? confirm;
  final String? confirmMessage;

  /// When non-null, [ConfirmSheet] renders an input gated on `text == typeToConfirm`.
  final String? typeToConfirm;
}

/// Per-action display metadata. Reads the live resource map for actions
/// whose label/message depends on current state (suspend's "Suspend"
/// vs "Resume", delete's owner-reference message).
ActionMeta getActionMeta(ActionId id, Map<String, dynamic> resource) {
  final metadata = resource['metadata'] as Map<String, dynamic>? ?? const {};
  final name = metadata['name'] as String? ?? '';
  switch (id) {
    case ActionId.scale:
      return const ActionMeta(label: 'Scale');
    case ActionId.restart:
      return const ActionMeta(
        label: 'Restart',
        confirm: 'confirm',
        confirmMessage:
            'This will perform a rolling restart, cycling all pods.',
      );
    case ActionId.delete:
      final owners =
          (metadata['ownerReferences'] as List?) ?? const <dynamic>[];
      final owner = owners.isNotEmpty ? owners.first as Map? : null;
      final kind = (resource['kind'] as String?) ?? 'resource';
      final msg = owner != null
          ? 'This $kind is managed by ${owner['kind']}/${owner['name']} and will be recreated after deletion.'
          : 'This will permanently delete "$name".';
      return ActionMeta(
        label: 'Delete',
        danger: true,
        confirm: 'destructive',
        confirmMessage: msg,
        typeToConfirm: name,
      );
    case ActionId.suspend:
      final spec = resource['spec'] as Map<String, dynamic>? ?? const {};
      final suspended = spec['suspend'] == true;
      return ActionMeta(
        label: suspended ? 'Resume' : 'Suspend',
        confirm: 'confirm',
        confirmMessage: suspended
            ? 'Resume scheduling/execution?'
            : 'Suspend scheduling/execution?',
      );
    case ActionId.trigger:
      return const ActionMeta(
        label: 'Trigger Job',
        confirm: 'confirm',
        confirmMessage: 'Create a new Job from this CronJob\'s template?',
      );
    case ActionId.rollback:
      return const ActionMeta(
        label: 'Rollback',
        confirm: 'confirm',
        confirmMessage: 'Pick a revision to roll back to.',
      );
  }
}

/// Result of a successful action. Carries a human-readable message for
/// snackbar rendering and the optional new-resource name for trigger.
class ActionResult {
  const ActionResult({required this.message, this.createdName});

  final String message;
  final String? createdName;
}

/// Execute an action against the backend. Throws [ApiError] on failure.
/// Mirrors the executeAction switch in action-handlers.ts.
Future<ActionResult> executeAction({
  required Dio dio,
  required ActionId id,
  required String kind,
  required String namespace,
  required String name,
  Map<String, dynamic>? params,
}) async {
  final base = '/api/v1/resources/$kind/$namespace/$name';
  try {
    switch (id) {
      case ActionId.scale:
        final replicas = params?['replicas'];
        if (replicas is! int || replicas < 0) {
          throw ApiError(
            statusCode: 400,
            code: 400,
            message: 'replicas must be a non-negative integer',
          );
        }
        await dio.post<Map<String, dynamic>>(
          '$base/scale',
          data: {'replicas': replicas},
        );
        return ActionResult(message: 'Scaled to $replicas replicas');
      case ActionId.restart:
        await dio.post<Map<String, dynamic>>('$base/restart');
        return const ActionResult(message: 'Rolling restart initiated');
      case ActionId.delete:
        await dio.delete<Map<String, dynamic>>(base);
        return ActionResult(message: 'Deleted $name');
      case ActionId.suspend:
        final suspend = params?['suspend'];
        if (suspend is! bool) {
          throw ApiError(
            statusCode: 400,
            code: 400,
            message: 'suspend must be a boolean',
          );
        }
        await dio.post<Map<String, dynamic>>(
          '$base/suspend',
          data: {'suspend': suspend},
        );
        return ActionResult(message: suspend ? 'Suspended' : 'Resumed');
      case ActionId.trigger:
        final res = await dio.post<Map<String, dynamic>>('$base/trigger');
        final data = res.data?['data'];
        final meta = data is Map ? data['metadata'] as Map? : null;
        final createdName = meta?['name'] as String?;
        return ActionResult(
          message: 'Job "${createdName ?? "unknown"}" created',
          createdName: createdName,
        );
      case ActionId.rollback:
        final revision = params?['revision'];
        if (revision is! int || revision <= 0) {
          throw ApiError(
            statusCode: 400,
            code: 400,
            message: 'revision must be a positive integer',
          );
        }
        await dio.post<Map<String, dynamic>>(
          '$base/rollback',
          data: {'revision': revision},
        );
        return ActionResult(message: 'Rolled back to revision $revision');
    }
  } on DioException catch (e) {
    final err = e.error;
    throw err is ApiError ? err : ApiError.fromDio(e);
  }
}

/// Convenience: read the active dio + run an action.
Future<ActionResult> runAction(
  Ref ref, {
  required ActionId id,
  required String kind,
  required String namespace,
  required String name,
  Map<String, dynamic>? params,
}) {
  return executeAction(
    dio: ref.read(dioProvider),
    id: id,
    kind: kind,
    namespace: namespace,
    name: name,
    params: params,
  );
}
