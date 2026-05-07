// Generic resource repository over GET /v1/resources/{kind}[/...].
// Backend serializes raw Kubernetes objects (corev1.Pod, appsv1.Deployment,
// etc.) under the canonical `{data:..., metadata:{total,continue}}`
// envelope. Mobile keeps the response as `Map<String,dynamic>` and lets
// per-kind models extract just the fields they render — full unstructured
// access stays available for the YAML tab.
//
// All requests carry the active cluster id via ClusterInterceptor; switching
// clusters invalidates the FutureProviders that depend on them.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';
import '../cluster/cluster_provider.dart';

/// Result of a list call. `total` mirrors the backend metadata; `nextPage`
/// is the opaque continue token (mobile doesn't paginate yet — surfaced
/// for a future infinite-scroll PR).
class ResourceList {
  const ResourceList({required this.items, required this.total, this.nextPage});

  final List<Map<String, dynamic>> items;
  final int total;
  final String? nextPage;
}

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
    CancelToken? cancelToken,
  }) async {
    final segments = <String>[
      'api',
      'v1',
      'resources',
      kind,
      if (namespace != null && namespace.isNotEmpty) namespace,
    ];
    final path = '/${segments.join('/')}';
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        path,
        queryParameters: {
          if (labelSelector != null && labelSelector.isNotEmpty)
            'labelSelector': labelSelector,
        },
        cancelToken: cancelToken,
      );
      final data = res.data?['data'];
      final metadata =
          res.data?['metadata'] as Map<String, dynamic>? ?? const {};
      final items = (data is List ? data : const <dynamic>[])
          .whereType<Map<dynamic, dynamic>>()
          .map<Map<String, dynamic>>(Map<String, dynamic>.from)
          .toList();
      return ResourceList(
        items: items,
        total: (metadata['total'] as num?)?.toInt() ?? items.length,
        nextPage: metadata['continue'] as String?,
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
    final path = namespace.isEmpty
        ? '/api/v1/resources/$kind/$name'
        : '/api/v1/resources/$kind/$namespace/$name';
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
}

final resourceRepositoryProvider = Provider<ResourceRepository>((ref) {
  return ResourceRepository(ref.watch(dioProvider));
});

/// Family provider for a kind+namespace list. Watches activeClusterProvider
/// so cluster switches refetch automatically.
final resourceListProvider = FutureProvider.autoDispose
    .family<ResourceList, ResourceListKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('list invalidated');
  });
  return ref.read(resourceRepositoryProvider).list(
        kind: key.kind,
        namespace: key.namespace,
        labelSelector: key.labelSelector,
        cancelToken: cancel,
      );
});

/// Family provider for a single resource fetch.
final resourceGetProvider = FutureProvider.autoDispose
    .family<Map<String, dynamic>, ResourceGetKey>((ref, key) async {
  ref.watch(activeClusterProvider);
  final cancel = CancelToken();
  ref.onDispose(() {
    if (!cancel.isCancelled) cancel.cancel('get invalidated');
  });
  return ref.read(resourceRepositoryProvider).get(
        kind: key.kind,
        namespace: key.namespace,
        name: key.name,
        cancelToken: cancel,
      );
});

/// Composite key used by [resourceListProvider] so the cache is keyed by
/// (kind, namespace, label-selector) tuple.
class ResourceListKey {
  const ResourceListKey({
    required this.kind,
    this.namespace,
    this.labelSelector,
  });

  final String kind;
  final String? namespace;
  final String? labelSelector;

  @override
  bool operator ==(Object other) =>
      other is ResourceListKey &&
      other.kind == kind &&
      other.namespace == namespace &&
      other.labelSelector == labelSelector;

  @override
  int get hashCode => Object.hash(kind, namespace, labelSelector);
}

class ResourceGetKey {
  const ResourceGetKey({
    required this.kind,
    required this.namespace,
    required this.name,
  });

  final String kind;
  final String namespace;
  final String name;

  @override
  bool operator ==(Object other) =>
      other is ResourceGetKey &&
      other.kind == kind &&
      other.namespace == namespace &&
      other.name == name;

  @override
  int get hashCode => Object.hash(kind, namespace, name);
}
