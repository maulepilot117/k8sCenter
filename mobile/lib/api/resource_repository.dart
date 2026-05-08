// Generic resource repository over GET /v1/resources/{kind}[/...].
// Backend serializes raw Kubernetes objects (corev1.Pod, appsv1.Deployment,
// etc.) under the canonical `{data:..., metadata:{total,continue}}`
// envelope. Mobile keeps the response as `Map<String,dynamic>` and lets
// per-kind models extract just the fields they render — full unstructured
// access stays available for the YAML tab.
//
// All requests carry the active cluster id via ClusterInterceptor; switching
// clusters invalidates the FutureProviders that depend on them.

import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';
import '../cluster/cluster_provider.dart';

/// Result of a list call. `total` mirrors the backend metadata.
/// `truncated` signals when the backend paginated and there are more
/// items than [items.length] — UI can show a "showing N of M" hint.
class ResourceList {
  const ResourceList({
    required this.items,
    required this.total,
    this.truncated = false,
  });

  final List<Map<String, dynamic>> items;
  final int total;
  final bool truncated;
}

/// Default per-list page cap. Keeps the tablet `DataTable` and phone
/// `ListView` from materializing thousands of rows on a busy cluster.
/// Backend's `paginateAny` honors `limit` query param; if more items
/// exist, we surface the truncation in the UI rather than silently
/// dropping them.
const int defaultListLimit = 200;

class ResourceRepository {
  ResourceRepository(this._dio);

  final Dio _dio;

  /// Lists resources of the given [kind]. When [namespace] is null, lists
  /// across all namespaces (cluster-scoped resources ignore namespace
  /// server-side). When [labelSelector] is provided, the backend filters
  /// the cache before responding.
  Future<ResourceList> list({
    required String kind,
    String? namespace,
    String? labelSelector,
    int limit = defaultListLimit,
    CancelToken? cancelToken,
    String? clusterIdOverride,
  }) async {
    // URL-encode each segment so a future kind/namespace containing
    // characters that need escaping (no current k8s name does, but
    // defensive) doesn't construct a malformed path.
    final segments = <String>[
      'api',
      'v1',
      'resources',
      Uri.encodeComponent(kind),
      if (namespace != null && namespace.isNotEmpty)
        Uri.encodeComponent(namespace),
    ];
    final path = '/${segments.join('/')}';
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        path,
        queryParameters: {
          if (labelSelector != null && labelSelector.isNotEmpty)
            'labelSelector': labelSelector,
          if (limit > 0) 'limit': '$limit',
        },
        // When a caller pins a specific cluster (wizard pickers — see
        // ClusterInterceptor's docstring) thread the override here so
        // the cache slot keyed on `clusterId` and the wire request
        // share the same X-Cluster-ID. Otherwise the interceptor falls
        // back to activeClusterProvider.
        options: clusterIdOverride == null
            ? null
            : Options(headers: {'X-Cluster-ID': clusterIdOverride}),
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      final metadata =
          res.data?['metadata'] as Map<String, dynamic>? ?? const {};
      final items = (data is List ? data : const <dynamic>[])
          .whereType<Map<dynamic, dynamic>>()
          .map<Map<String, dynamic>>(Map<String, dynamic>.from)
          .toList();
      final total = (metadata['total'] as num?)?.toInt() ?? items.length;
      return ResourceList(
        items: items,
        total: total,
        truncated: total > items.length,
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Fetches a single resource. For cluster-scoped kinds, pass `''` for
  /// [namespace] (the backend's `extractNsName` swaps namespace and name
  /// based on the adapter's ClusterScoped flag).
  Future<Map<String, dynamic>> get({
    required String kind,
    required String namespace,
    required String name,
    CancelToken? cancelToken,
  }) async {
    // Cluster-scoped resources hit /resources/{kind}/{name}; namespaced
    // resources hit /resources/{kind}/{namespace}/{name}. The backend
    // routes are split on `/{namespace}/{name}` vs `/{name}` for cluster
    // kinds, so empty namespace becomes the single-segment path.
    final segs = <String>[
      'api',
      'v1',
      'resources',
      Uri.encodeComponent(kind),
      if (namespace.isNotEmpty) Uri.encodeComponent(namespace),
      Uri.encodeComponent(name),
    ];
    final path = '/${segs.join('/')}';
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        path,
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      if (data is! Map) {
        throw ApiError(
          statusCode: 500,
          code: 500,
          message: 'resource response missing data',
        );
      }
      return Map<String, dynamic>.from(data);
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) rethrow;
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  /// Fetches a single Secret data value via the per-key audited reveal
  /// endpoint. Each call is logged server-side as a distinct reveal
  /// action — the GET on the parent resource doesn't carry the same
  /// audit weight (it returns base64; reveal returns plaintext).
  Future<String> revealSecretKey({
    required String namespace,
    required String name,
    required String key,
  }) async {
    final path =
        '/api/v1/resources/secrets/${Uri.encodeComponent(namespace)}/${Uri.encodeComponent(name)}/reveal/${Uri.encodeComponent(key)}';
    try {
      final res = await _dio.get<Map<String, dynamic>>(path);
      final data = res.data?['data'];
      if (data is Map && data['value'] is String) {
        return data['value'] as String;
      }
      // Some adapter shapes return the bare string under data.
      if (data is String) return data;
      throw ApiError(
        statusCode: 500,
        code: 500,
        message: 'reveal response missing value',
      );
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final resourceRepositoryProvider = Provider<ResourceRepository>((ref) {
  return ResourceRepository(ref.watch(dioProvider));
});

/// Family provider for a kind+namespace list. The family key carries
/// the active cluster id, so swapping clusters keys a fresh entry
/// (rather than reusing the prior cluster's cache). The token cancels
/// in-flight requests on dispose; cancel exceptions are swallowed via
/// a never-completing future so the UI never sees a "cancelled" error.
final resourceListProvider = FutureProvider.autoDispose
    .family<ResourceList, ResourceListKey>((ref, key) async {
  // Watch active cluster so the autoDispose tear-down fires on switch
  // even though clusterId is also keyed into ResourceListKey by callers.
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('list invalidated');
  });
  try {
    return await ref.read(resourceRepositoryProvider).list(
          kind: key.kind,
          namespace: key.namespace,
          labelSelector: key.labelSelector,
          cancelToken: cancel,
          // The cache slot is keyed on key.clusterId. Pin the wire
          // request to the same cluster so a mid-request switch of
          // activeClusterProvider doesn't populate this slot with
          // wrong-cluster data — the bug that bit wizard pickers in
          // M3 PR-3c review.
          clusterIdOverride: key.clusterId,
        );
  } on DioException catch (e) {
    if (CancelToken.isCancel(e)) {
      // Block forever — the autoDispose teardown that triggered the
      // cancel will dispose this provider before the future resolves,
      // so the .when error branch never sees the cancel exception.
      return Completer<ResourceList>().future;
    }
    rethrow;
  }
});

/// Family provider for a single resource fetch. Same race-safety
/// pattern as [resourceListProvider].
final resourceGetProvider = FutureProvider.autoDispose
    .family<Map<String, dynamic>, ResourceGetKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('get invalidated');
  });
  try {
    return await ref.read(resourceRepositoryProvider).get(
          kind: key.kind,
          namespace: key.namespace,
          name: key.name,
          cancelToken: cancel,
        );
  } on DioException catch (e) {
    if (CancelToken.isCancel(e)) {
      return Completer<Map<String, dynamic>>().future;
    }
    rethrow;
  }
});

/// Composite key used by [resourceListProvider] so the cache is keyed by
/// (clusterId, kind, namespace, label-selector) tuple. ClusterId is
/// load-bearing: without it, switching from cluster A to B would reuse
/// A's cached entries for the same (kind, ns) under B's `X-Cluster-ID`
/// header — visually plausible but wrong-cluster data.
class ResourceListKey {
  const ResourceListKey({
    required this.clusterId,
    required this.kind,
    this.namespace,
    this.labelSelector,
  });

  final String clusterId;
  final String kind;
  final String? namespace;
  final String? labelSelector;

  @override
  bool operator ==(Object other) =>
      other is ResourceListKey &&
      other.clusterId == clusterId &&
      other.kind == kind &&
      other.namespace == namespace &&
      other.labelSelector == labelSelector;

  @override
  int get hashCode => Object.hash(clusterId, kind, namespace, labelSelector);
}

class ResourceGetKey {
  const ResourceGetKey({
    required this.clusterId,
    required this.kind,
    required this.namespace,
    required this.name,
  });

  final String clusterId;
  final String kind;
  final String namespace;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is ResourceGetKey &&
      other.clusterId == clusterId &&
      other.kind == kind &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(clusterId, kind, namespace, name);
}
