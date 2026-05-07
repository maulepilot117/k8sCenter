// Resource action handlers — Dart port of `frontend/lib/action-handlers.ts`.
//
// Kept structurally isomorphic to the TS source so action drift between web
// and mobile is easy to spot in a diff. When the TS file gains a new action,
// kind, or verb, this file is the parallel edit.
//
// Action verbs hit `POST /api/v1/resources/:kind/:ns/:name/<verb>` (or
// `DELETE` for delete) — see `backend/internal/server/routes.go:758-762`.

import 'package:dio/dio.dart';

import '../auth/permissions.dart';
import '../auth/user.dart';
import 'api_error.dart';

/// Valid action identifiers. Mirrors `ActionId` in action-handlers.ts.
enum ActionId { scale, restart, delete, suspend, trigger, rollback }

/// Kind → action map. Mirrors `frontend/lib/action-handlers.ts:ACTIONS_BY_KIND`
/// 1:1 except where M2 intentionally defers an action. Drift between this
/// map and the TS source is the bug class type-to-confirm exists to
/// prevent — keep them isomorphic.
///
/// Deferred:
///   - rolebindings / clusterrolebindings delete (cluster-scoped URL
///     routing not yet implemented; current `executeAction` URL builder
///     assumes namespaced resources, which produces a malformed
///     `/.../<kind>//<name>` for cluster-scoped delete. Tracked for M3.)
///   - nodes (delete is not a routine oncall verb; deferred per master plan.)
const Map<String, List<ActionId>> actionsByKind = {
  'deployments': [
    ActionId.scale,
    ActionId.restart,
    ActionId.rollback,
    ActionId.delete,
  ],
  'statefulsets': [ActionId.scale, ActionId.restart, ActionId.delete],
  'daemonsets': [ActionId.restart, ActionId.delete],
  'replicasets': [ActionId.delete],
  'pods': [ActionId.delete],
  'jobs': [ActionId.suspend, ActionId.delete],
  'cronjobs': [ActionId.suspend, ActionId.trigger, ActionId.delete],
  'services': [ActionId.delete],
  'ingresses': [ActionId.delete],
  'configmaps': [ActionId.delete],
  'secrets': [ActionId.delete],
  'persistentvolumeclaims': [ActionId.delete],
  'namespaces': [ActionId.delete],
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
///
/// The destructive-vs-simple confirm distinction is encoded entirely by
/// `typeToConfirm`: non-null means destructive (ConfirmSheet renders a
/// type-to-confirm input gating the confirm button), null means simple
/// OK/Cancel. The TS source's `confirm: 'confirm' | 'destructive'`
/// discriminator was redundant in Dart and has been dropped.
class ActionMeta {
  const ActionMeta({
    required this.label,
    this.danger = false,
    this.confirmMessage,
    this.typeToConfirm,
  });

  final String label;
  final bool danger;
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
        confirmMessage:
            'This will perform a rolling restart, cycling all pods.',
      );
    case ActionId.delete:
      final owners =
          (metadata['ownerReferences'] as List?) ?? const <dynamic>[];
      // Defensive `is Map` guard — a bare `as Map?` cast would throw
      // CastError synchronously inside the action sheet build for a
      // malformed payload (e.g., backend returns a string in the array).
      final owner = (owners.isNotEmpty && owners.first is Map)
          ? owners.first as Map
          : null;
      final kind = (resource['kind'] as String?) ?? 'resource';
      final msg = owner != null
          ? 'This $kind is managed by ${owner['kind']}/${owner['name']} and will be recreated after deletion.'
          : 'This will permanently delete "$name".';
      return ActionMeta(
        label: 'Delete',
        danger: true,
        confirmMessage: msg,
        typeToConfirm: name,
      );
    case ActionId.suspend:
      final spec = resource['spec'] as Map<String, dynamic>? ?? const {};
      final suspended = spec['suspend'] == true;
      return ActionMeta(
        label: suspended ? 'Resume' : 'Suspend',
        confirmMessage: suspended
            ? 'Resume scheduling/execution?'
            : 'Suspend scheduling/execution?',
      );
    case ActionId.trigger:
      return const ActionMeta(
        label: 'Trigger Job',
        confirmMessage: 'Create a new Job from this CronJob\'s template?',
      );
    case ActionId.rollback:
      return const ActionMeta(
        label: 'Rollback',
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

/// Per-action receive timeout. Defaults match Dio's 30s; delete bumps to
/// 90s because pods with `terminationGracePeriodSeconds > 30` (or
/// namespace deletes with cascading dependents) regularly take longer
/// than 30s before the backend's k8s API call returns. Without this, a
/// long-grace delete surfaces as `Network error` even though the
/// deletion is proceeding.
const Duration _defaultActionTimeout = Duration(seconds: 30);
const Duration _deleteActionTimeout = Duration(seconds: 90);

/// Build the `/api/v1/resources/...` base path. Skips the namespace
/// segment for cluster-scoped resources (namespace is empty), so an
/// action on a Namespace produces `/api/v1/resources/namespaces/<name>`
/// not `/api/v1/resources/namespaces//<name>`. Matches the backend
/// router's split between cluster-scoped and namespaced action routes.
String _resourceBase(String kind, String namespace, String name) {
  final segs = <String>[
    'api',
    'v1',
    'resources',
    Uri.encodeComponent(kind),
    if (namespace.isNotEmpty) Uri.encodeComponent(namespace),
    Uri.encodeComponent(name),
  ];
  return '/${segs.join('/')}';
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
  final base = _resourceBase(kind, namespace, name);
  final opts = Options(
    receiveTimeout:
        id == ActionId.delete ? _deleteActionTimeout : _defaultActionTimeout,
  );
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
          options: opts,
        );
        return ActionResult(message: 'Scaled to $replicas replicas');
      case ActionId.restart:
        await dio.post<Map<String, dynamic>>('$base/restart', options: opts);
        return const ActionResult(message: 'Rolling restart initiated');
      case ActionId.delete:
        await dio.delete<Map<String, dynamic>>(base, options: opts);
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
          options: opts,
        );
        return ActionResult(message: suspend ? 'Suspended' : 'Resumed');
      case ActionId.trigger:
        final res =
            await dio.post<Map<String, dynamic>>('$base/trigger', options: opts);
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
          options: opts,
        );
        return ActionResult(message: 'Rolled back to revision $revision');
    }
  } on DioException catch (e) {
    final err = e.error;
    throw err is ApiError ? err : ApiError.fromDio(e);
  }
}

