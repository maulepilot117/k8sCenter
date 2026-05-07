// Lists registered clusters via /v1/clusters. Cached as a FutureProvider
// so the picker reuses the same fetch. Refreshed on bottom-sheet open.
//
// The local cluster is implicit even if /v1/clusters returns empty —
// the backend ClusterRouter always answers requests with no
// X-Cluster-ID against the local cluster. Single-cluster homelabs
// typically don't bother registering anything.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../api/dio_client.dart';
import 'cluster.dart';

/// Synthetic stand-in shown when the backend's `/v1/clusters` is unreachable
/// or returns an empty list. Status string and display name match what
/// `backend/internal/store/clusters.go::EnsureLocal` would persist for the
/// real local entry, so UI branching on the literal status renders the
/// fallback identically to a populated list.
const Cluster localCluster = Cluster(
  id: 'local',
  name: 'local',
  displayName: 'Local Cluster',
  isLocal: true,
  status: 'connected',
);

/// Result of [ClusterRepository.list]. Carries the cluster list plus a
/// note when the backend was unreachable so the picker can render a
/// degraded-mode hint instead of silently masking a real outage.
class ClusterListResult {
  const ClusterListResult({required this.clusters, this.degraded = false});

  final List<Cluster> clusters;

  /// True when the backend was unreachable (true network failure) and the
  /// list contains only the synthetic [localCluster] fallback. Backend HTTP
  /// errors (401/403/5xx) propagate as [ApiError] instead of degrading.
  final bool degraded;
}

class ClusterRepository {
  ClusterRepository(this._dio);

  final Dio _dio;

  Future<ClusterListResult> list() async {
    try {
      final res = await _dio.get<Map<String, dynamic>>('/api/v1/clusters');
      final data = res.data?['data'];
      final list = (data is List ? data : const <dynamic>[])
          .whereType<Map<String, dynamic>>()
          .map(Cluster.fromJson)
          .toList();
      // Guarantee a 'local' entry — backend may omit it on minimal deployments.
      if (!list.any((c) => c.isLocal || c.id == 'local')) {
        list.insert(0, localCluster);
      }
      return ClusterListResult(clusters: list);
    } on DioException catch (e) {
      // True network failure (DNS, connection refused, timeout) → degrade
      // to local-only so the UI still works for a homelab. Authoritative
      // HTTP errors (401/403/5xx) bubble up as ApiError so the picker can
      // surface the real cause instead of pretending "single cluster".
      if (_isNetworkFailure(e)) {
        return const ClusterListResult(
          clusters: [localCluster],
          degraded: true,
        );
      }
      final err = e.error;
      if (err is ApiError) throw err;
      throw ApiError.fromDio(e);
    }
  }

  static bool _isNetworkFailure(DioException e) {
    switch (e.type) {
      case DioExceptionType.connectionTimeout:
      case DioExceptionType.sendTimeout:
      case DioExceptionType.receiveTimeout:
      case DioExceptionType.connectionError:
        return true;
      case DioExceptionType.badResponse:
      case DioExceptionType.cancel:
      case DioExceptionType.badCertificate:
      case DioExceptionType.unknown:
        return false;
    }
  }
}

final clusterRepositoryProvider = Provider<ClusterRepository>((ref) {
  return ClusterRepository(ref.watch(dioProvider));
});

/// Cached cluster list result. The picker calls
/// `ref.invalidate(clustersProvider)` from `ClusterPickerSheet.show` to
/// force a fresh fetch on bottom-sheet open so users selecting a cluster
/// always see the current state.
final clustersProvider = FutureProvider<ClusterListResult>((ref) async {
  return ref.read(clusterRepositoryProvider).list();
});
