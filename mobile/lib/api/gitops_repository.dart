// Mobile-side wrapper over the backend GitOps API
// (`/v1/gitops/{status,applications,applications/{id},commits,
// applicationsets,applicationsets/{id}}`). The wire types mirror
// `frontend/lib/gitops-types.ts` and `backend/internal/gitops/types.go`.
//
// Read surfaces only — verb actions (sync/suspend/rollback/
// refresh/delete) are not modelled here and are deferred to a later
// milestone.
//
// Composite ID:
//   * The backend assigns each app an opaque tool-prefixed id —
//     "argo:ns:name", "flux-ks:ns:name", "flux-hr:ns:name". Mobile
//     never reconstructs this; it round-trips the `id` field through
//     `GitOpsId.tryParse` / `encode` only for go_router path safety.
//   * AppSets are Argo CD-only and use the "argo-as:ns:name" shape.
//
// Cluster pinning: every call accepts `clusterIdOverride` and forwards
// it as an explicit `X-Cluster-ID` header so the family-keyed cache
// slot and the wire request always agree.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import 'api_error.dart';
import 'dio_client.dart';

/// Per-tool availability flags from `/v1/gitops/status`. Mirrors
/// `backend/internal/gitops/types.go::ToolDetail` exactly.
class GitOpsToolDetail {
  const GitOpsToolDetail({
    required this.available,
    this.namespace,
    this.controllers = const <String>[],
    this.appSetsAvailable = false,
    this.notificationAvailable = false,
  });

  final bool available;
  final String? namespace;

  /// Flux-only: the set of installed Flux controllers
  /// (`source`, `kustomize`, `helm`, `notification`). Empty for Argo.
  final List<String> controllers;

  /// Argo-only: whether the ApplicationSet CRD is installed. Gates the
  /// drawer entry for the AppSets list per plan U5.
  final bool appSetsAvailable;
  final bool notificationAvailable;

  factory GitOpsToolDetail.fromJson(Map<String, dynamic> json) {
    final raw = json['controllers'];
    return GitOpsToolDetail(
      available: json['available'] as bool? ?? false,
      namespace: json['namespace'] as String?,
      controllers: raw is List
          ? raw.whereType<String>().toList()
          : const <String>[],
      appSetsAvailable: json['appSetsAvailable'] as bool? ?? false,
      notificationAvailable: json['notificationAvailable'] as bool? ?? false,
    );
  }

  static const empty = GitOpsToolDetail(available: false);
}

/// Decoded `/v1/gitops/status` response. `detected` is one of `""`,
/// `"argocd"`, `"fluxcd"`, `"both"`. `isInstalled` is the gate for
/// `FeatureUnavailableState.gitops()`.
class GitOpsStatus {
  const GitOpsStatus({
    required this.detected,
    required this.argoCD,
    required this.fluxCD,
    this.lastChecked,
  });

  /// `""` when neither tool is detected.
  final String detected;
  final GitOpsToolDetail argoCD;
  final GitOpsToolDetail fluxCD;
  final String? lastChecked;

  bool get isInstalled => detected.isNotEmpty;
  bool get hasArgo => detected == 'argocd' || detected == 'both';
  bool get hasFlux => detected == 'fluxcd' || detected == 'both';

  factory GitOpsStatus.fromJson(Map<String, dynamic> json) {
    final argo = json['argocd'];
    final flux = json['fluxcd'];
    return GitOpsStatus(
      detected: json['detected'] as String? ?? '',
      argoCD: argo is Map
          ? GitOpsToolDetail.fromJson(Map<String, dynamic>.from(argo))
          : GitOpsToolDetail.empty,
      fluxCD: flux is Map
          ? GitOpsToolDetail.fromJson(Map<String, dynamic>.from(flux))
          : GitOpsToolDetail.empty,
      lastChecked: json['lastChecked'] as String?,
    );
  }

  static const empty = GitOpsStatus(
    detected: '',
    argoCD: GitOpsToolDetail.empty,
    fluxCD: GitOpsToolDetail.empty,
  );
}

/// Where an application's manifests come from (git path / helm chart).
class AppSource {
  const AppSource({
    this.repoURL,
    this.path,
    this.targetRevision,
    this.chartName,
    this.chartVersion,
  });

  final String? repoURL;
  final String? path;
  final String? targetRevision;
  final String? chartName;
  final String? chartVersion;

  factory AppSource.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return AppSource(
      repoURL: s(json['repoURL']),
      path: s(json['path']),
      targetRevision: s(json['targetRevision']),
      chartName: s(json['chartName']),
      chartVersion: s(json['chartVersion']),
    );
  }
}

/// Normalised Argo Application / Flux Kustomization / Flux HelmRelease.
class NormalizedApp {
  const NormalizedApp({
    required this.id,
    required this.name,
    required this.namespace,
    required this.tool,
    required this.kind,
    required this.syncStatus,
    required this.healthStatus,
    required this.source,
    this.currentRevision,
    this.lastSyncTime,
    this.message,
    this.destinationCluster,
    this.destinationNamespace,
    this.managedResourceCount = 0,
    this.suspended = false,
  });

  /// Backend-issued composite id: `"argocd:ns:name"`,
  /// `"flux-ks:ns:name"`, or `"flux-hr:ns:name"`. The leading segment
  /// (the `tool` prefix in [GitOpsId]) is the source of truth for tab
  /// visibility — HelmRelease hides Resources + History.
  final String id;

  final String name;
  final String namespace;

  /// `"argocd"`, `"fluxcd"`, `"both"`, or `""`. Display-only; do not
  /// branch tab visibility on this — use the `id` prefix instead.
  final String tool;

  /// Canonical CRD kind (`Application`, `Kustomization`, `HelmRelease`).
  final String kind;

  /// Normalised sync state. Strings rather than an enum so unknown
  /// backend values render verbatim rather than collapsing to "unknown".
  final String syncStatus;
  final String healthStatus;

  final AppSource source;
  final String? currentRevision;
  final String? lastSyncTime;
  final String? message;
  final String? destinationCluster;
  final String? destinationNamespace;
  final int managedResourceCount;
  final bool suspended;

  factory NormalizedApp.fromJson(Map<String, dynamic> json) {
    final src = json['source'];
    return NormalizedApp(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      tool: json['tool'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      syncStatus: json['syncStatus'] as String? ?? 'unknown',
      healthStatus: json['healthStatus'] as String? ?? 'unknown',
      source: src is Map
          ? AppSource.fromJson(Map<String, dynamic>.from(src))
          : const AppSource(),
      currentRevision: json['currentRevision'] as String?,
      lastSyncTime: json['lastSyncTime'] as String?,
      message: json['message'] as String?,
      destinationCluster: json['destinationCluster'] as String?,
      destinationNamespace: json['destinationNamespace'] as String?,
      managedResourceCount:
          (json['managedResourceCount'] as num?)?.toInt() ?? 0,
      suspended: json['suspended'] as bool? ?? false,
    );
  }

  /// Detail-tab gate. HelmRelease hides Resources + History because the
  /// Flux HelmRelease CRD's `status.history` and managed-resource
  /// inventory aren't queryable through the same backend code path.
  bool get hidesResourcesAndHistory => id.startsWith('flux-hr:');
}

/// One row in the Resources tab. `health` is optional because Flux
/// Kustomizations don't carry per-object health, only sync state.
class ManagedResource {
  const ManagedResource({
    this.group,
    required this.kind,
    this.namespace,
    required this.name,
    required this.status,
    this.health,
  });

  final String? group;
  final String kind;
  final String? namespace;
  final String name;
  final String status;
  final String? health;

  factory ManagedResource.fromJson(Map<String, dynamic> json) {
    String? s(Object? v) => v is String && v.isNotEmpty ? v : null;
    return ManagedResource(
      group: s(json['group']),
      kind: json['kind'] as String? ?? '',
      namespace: s(json['namespace']),
      name: json['name'] as String? ?? '',
      status: json['status'] as String? ?? '',
      health: s(json['health']),
    );
  }
}

/// One row in the History tab.
class RevisionEntry {
  const RevisionEntry({
    required this.revision,
    required this.status,
    this.message,
    required this.deployedAt,
  });

  final String revision;
  final String status;
  final String? message;
  final String deployedAt;

  factory RevisionEntry.fromJson(Map<String, dynamic> json) {
    return RevisionEntry(
      revision: json['revision'] as String? ?? '',
      status: json['status'] as String? ?? '',
      message: json['message'] as String?,
      deployedAt: json['deployedAt'] as String? ?? '',
    );
  }
}

/// Full detail envelope returned from `/v1/gitops/applications/{id}`.
/// `resources` + `history` are nullable rather than empty for
/// HelmRelease detail responses, where the backend omits the fields
/// entirely — null is the signal to hide the tab, not "empty list".
class AppDetail {
  const AppDetail({
    required this.app,
    this.resources,
    this.history,
  });

  final NormalizedApp app;
  final List<ManagedResource>? resources;
  final List<RevisionEntry>? history;

  factory AppDetail.fromJson(Map<String, dynamic> json) {
    List<T>? optList<T>(Object? raw, T Function(Map<String, dynamic>) parse) {
      if (raw is! List) return null;
      return raw
          .whereType<Map<dynamic, dynamic>>()
          .map((m) => parse(Map<String, dynamic>.from(m)))
          .toList();
    }

    final rawApp = json['app'];
    return AppDetail(
      app: rawApp is Map
          ? NormalizedApp.fromJson(Map<String, dynamic>.from(rawApp))
          : const NormalizedApp(
              id: '',
              name: '',
              namespace: '',
              tool: '',
              kind: '',
              syncStatus: 'unknown',
              healthStatus: 'unknown',
              source: AppSource(),
            ),
      resources: optList(json['resources'], ManagedResource.fromJson),
      history: optList(json['history'], RevisionEntry.fromJson),
    );
  }
}

/// Aggregate counts on the Applications list. Drives the summary chip
/// row at the top of the list screen.
class AppListMetadata {
  const AppListMetadata({
    this.total = 0,
    this.synced = 0,
    this.outOfSync = 0,
    this.degraded = 0,
    this.progressing = 0,
    this.suspended = 0,
  });

  final int total;
  final int synced;
  final int outOfSync;
  final int degraded;
  final int progressing;
  final int suspended;

  factory AppListMetadata.fromJson(Map<String, dynamic> json) {
    int n(String k) => (json[k] as num?)?.toInt() ?? 0;
    return AppListMetadata(
      total: n('total'),
      synced: n('synced'),
      outOfSync: n('outOfSync'),
      degraded: n('degraded'),
      progressing: n('progressing'),
      suspended: n('suspended'),
    );
  }

  static const empty = AppListMetadata();
}

/// `/v1/gitops/applications` response envelope.
class AppListResponse {
  const AppListResponse({
    required this.applications,
    required this.summary,
  });

  final List<NormalizedApp> applications;
  final AppListMetadata summary;

  factory AppListResponse.fromJson(Map<String, dynamic> json) {
    final raw = json['applications'];
    final summary = json['summary'];
    return AppListResponse(
      applications: raw is List
          ? raw
              .whereType<Map<dynamic, dynamic>>()
              .map((m) => NormalizedApp.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <NormalizedApp>[],
      summary: summary is Map
          ? AppListMetadata.fromJson(Map<String, dynamic>.from(summary))
          : AppListMetadata.empty,
    );
  }
}

/// Normalised ApplicationSet. Argo-only; gated on
/// `status.argoCD.appSetsAvailable`.
class NormalizedAppSet {
  const NormalizedAppSet({
    required this.id,
    required this.name,
    required this.namespace,
    required this.tool,
    required this.generatorTypes,
    required this.templateSource,
    required this.templateDestination,
    required this.status,
    this.statusMessage,
    this.generatedAppCount = 0,
    this.summary = AppListMetadata.empty,
    this.preserveOnDeletion = false,
    this.createdAt = '',
  });

  final String id;
  final String name;
  final String namespace;

  /// Always `"argocd"` today, but the field is kept polymorphic to
  /// mirror the backend envelope rather than special-case it.
  final String tool;

  final List<String> generatorTypes;
  final AppSource templateSource;
  final String templateDestination;
  final String status;
  final String? statusMessage;
  final int generatedAppCount;
  final AppListMetadata summary;
  final bool preserveOnDeletion;
  final String createdAt;

  factory NormalizedAppSet.fromJson(Map<String, dynamic> json) {
    final gens = json['generatorTypes'];
    final src = json['templateSource'];
    final summary = json['summary'];
    return NormalizedAppSet(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      namespace: json['namespace'] as String? ?? '',
      tool: json['tool'] as String? ?? 'argocd',
      generatorTypes: gens is List
          ? gens.whereType<String>().toList()
          : const <String>[],
      templateSource: src is Map
          ? AppSource.fromJson(Map<String, dynamic>.from(src))
          : const AppSource(),
      templateDestination: json['templateDestination'] as String? ?? '',
      status: json['status'] as String? ?? '',
      statusMessage: json['statusMessage'] as String?,
      generatedAppCount: (json['generatedAppCount'] as num?)?.toInt() ?? 0,
      summary: summary is Map
          ? AppListMetadata.fromJson(Map<String, dynamic>.from(summary))
          : AppListMetadata.empty,
      preserveOnDeletion: json['preserveOnDeletion'] as bool? ?? false,
      createdAt: json['createdAt'] as String? ?? '',
    );
  }
}

/// One row in the Conditions panel on the AppSet detail screen.
class AppSetCondition {
  const AppSetCondition({
    required this.type,
    required this.status,
    this.message,
    this.reason,
  });

  final String type;
  final String status;
  final String? message;
  final String? reason;

  bool get isError =>
      status == 'True' && type.toLowerCase().contains('error');

  factory AppSetCondition.fromJson(Map<String, dynamic> json) {
    return AppSetCondition(
      type: json['type'] as String? ?? '',
      status: json['status'] as String? ?? '',
      message: json['message'] as String?,
      reason: json['reason'] as String?,
    );
  }
}

/// Full AppSet detail envelope. `generators` is the raw Argo CD CRD
/// shape (a list of generator-block maps); rendering is left to the
/// detail screen which expand/collapses the JSON inline.
class AppSetDetail {
  const AppSetDetail({
    required this.appSet,
    required this.generators,
    required this.conditions,
    required this.applications,
  });

  final NormalizedAppSet appSet;
  final List<Map<String, Object?>> generators;
  final List<AppSetCondition> conditions;
  final List<NormalizedApp> applications;

  factory AppSetDetail.fromJson(Map<String, dynamic> json) {
    final rawGens = json['generators'];
    final rawConds = json['conditions'];
    final rawApps = json['applications'];
    final appSet = json['appSet'];
    return AppSetDetail(
      appSet: appSet is Map
          ? NormalizedAppSet.fromJson(Map<String, dynamic>.from(appSet))
          : const NormalizedAppSet(
              id: '',
              name: '',
              namespace: '',
              tool: 'argocd',
              generatorTypes: <String>[],
              templateSource: AppSource(),
              templateDestination: '',
              status: '',
            ),
      generators: rawGens is List
          ? rawGens
              .whereType<Map<dynamic, dynamic>>()
              .map((m) => Map<String, Object?>.from(m))
              .toList()
          : const <Map<String, Object?>>[],
      conditions: rawConds is List
          ? rawConds
              .whereType<Map<dynamic, dynamic>>()
              .map((m) =>
                  AppSetCondition.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <AppSetCondition>[],
      applications: rawApps is List
          ? rawApps
              .whereType<Map<dynamic, dynamic>>()
              .map((m) =>
                  NormalizedApp.fromJson(Map<String, dynamic>.from(m)))
              .toList()
          : const <NormalizedApp>[],
    );
  }
}

/// Mobile wrapper over `/v1/gitops/*`. Stateless — cluster pinning
/// threads through `clusterIdOverride` so the wire header always
/// matches the family-key slot the controller writes back into.
class GitOpsRepository {
  GitOpsRepository(this._dio);

  final Dio _dio;

  /// Fetches GitOps discovery status. Returns [GitOpsStatus.empty] on
  /// 5xx so callers route straight to `FeatureUnavailableState.gitops()`
  /// — a flaky reverse-proxy probe should not surface as an error card.
  Future<GitOpsStatus> status({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/gitops/status',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return GitOpsStatus.fromJson(Map<String, dynamic>.from(data));
      }
      return GitOpsStatus.empty;
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final code = e.response?.statusCode ?? 0;
      if (code >= 500 && code < 600) return GitOpsStatus.empty;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Fetches the full Applications list with summary metadata.
  Future<AppListResponse> listApplications({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/gitops/applications',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return AppListResponse.fromJson(Map<String, dynamic>.from(data));
      }
      return const AppListResponse(
        applications: <NormalizedApp>[],
        summary: AppListMetadata.empty,
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Fetches a single application's detail by composite id. The id is
  /// URL-encoded here once — callers must pass the raw "tool:ns:name"
  /// string, not a pre-encoded one (double-encoding bug fixed in
  /// backend commit 7c6fa14).
  Future<AppDetail> getApplication({
    required String id,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/gitops/applications/${Uri.encodeComponent(id)}',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return AppDetail.fromJson(Map<String, dynamic>.from(data));
      }
      throw ApiError(
        statusCode: 500,
        code: 500,
        message: 'Empty response for $id',
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Fetches the AppSets list (Argo-only). Returns an empty list if
  /// the cluster has no AppSet CRD — the backend handler is gated on
  /// `appSetsAvailable` and returns 404 / 503 when missing.
  Future<List<NormalizedAppSet>> listApplicationSets({
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/gitops/applicationsets',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        // Backend envelope key is camelCase `applicationSets` per
        // handler.go::HandleListAppSets. The lowercase form is the URL
        // segment, NOT the JSON field name.
        final raw = data['applicationSets'];
        if (raw is List) {
          return raw
              .whereType<Map<dynamic, dynamic>>()
              .map((m) =>
                  NormalizedAppSet.fromJson(Map<String, dynamic>.from(m)))
              .toList();
        }
      }
      return const <NormalizedAppSet>[];
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Fetches a single AppSet's detail.
  Future<AppSetDetail> getApplicationSet({
    required String id,
    String? clusterIdOverride,
    CancelToken? cancelToken,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/gitops/applicationsets/${Uri.encodeComponent(id)}',
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is Map) {
        return AppSetDetail.fromJson(Map<String, dynamic>.from(data));
      }
      throw ApiError(
        statusCode: 500,
        code: 500,
        message: 'Empty response for ApplicationSet $id',
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final gitOpsRepositoryProvider = Provider<GitOpsRepository>((ref) {
  return GitOpsRepository(ref.watch(dioProvider));
});

/// Per-cluster GitOps status. Drives `FeatureUnavailableState.gitops()`
/// and the AppSets-list drawer gate. Keyed on cluster id so a cluster
/// switch keys a fresh entry rather than reusing the prior cluster's
/// gate decision.
final gitOpsStatusProvider = FutureProvider.autoDispose
    .family<GitOpsStatus, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('status invalidated');
  });
  return ref.read(gitOpsRepositoryProvider).status(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

/// Per-cluster GitOps applications list. Keyed on cluster id so cluster
/// switches don't replay the prior cluster's applications into the new
/// active cluster's surface.
final gitOpsApplicationsProvider = FutureProvider.autoDispose
    .family<AppListResponse, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('applications invalidated');
  });
  return ref.read(gitOpsRepositoryProvider).listApplications(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

/// Family key for the application-detail provider. Carries both
/// cluster id (so cluster switches force a fresh slot) and composite id
/// (so two open detail screens don't share state).
class GitOpsAppKey {
  const GitOpsAppKey({required this.clusterId, required this.id});

  final String clusterId;
  final String id;

  @override
  bool operator ==(Object other) =>
      other is GitOpsAppKey && other.clusterId == clusterId && other.id == id;

  @override
  int get hashCode => Object.hash(clusterId, id);
}

final gitOpsApplicationDetailProvider = FutureProvider.autoDispose
    .family<AppDetail, GitOpsAppKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('detail invalidated');
  });
  return ref.read(gitOpsRepositoryProvider).getApplication(
        id: key.id,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});

final gitOpsApplicationSetsProvider = FutureProvider.autoDispose
    .family<List<NormalizedAppSet>, String>((ref, clusterId) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('appsets invalidated');
  });
  return ref.read(gitOpsRepositoryProvider).listApplicationSets(
        clusterIdOverride: clusterId,
        cancelToken: cancel,
      );
});

final gitOpsApplicationSetDetailProvider = FutureProvider.autoDispose
    .family<AppSetDetail, GitOpsAppKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('appset detail invalidated');
  });
  return ref.read(gitOpsRepositoryProvider).getApplicationSet(
        id: key.id,
        clusterIdOverride: key.clusterId,
        cancelToken: cancel,
      );
});
