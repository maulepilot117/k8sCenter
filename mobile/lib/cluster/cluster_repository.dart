// Lists registered clusters via /v1/clusters. Cached as a FutureProvider
// so the picker reuses the same fetch. Refreshed on bottom-sheet open.
//
// The local cluster is implicit even if /v1/clusters returns empty —
// the backend ClusterRouter always answers requests with no
// X-Cluster-ID against the local cluster. Single-cluster homelabs
// typically don't bother registering anything.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/dio_client.dart';
import 'cluster.dart';

const Cluster localCluster = Cluster(
  id: 'local',
  name: 'local',
  isLocal: true,
  status: 'ready',
);

class ClusterRepository {
  ClusterRepository(this._dio);

  final Dio _dio;

  Future<List<Cluster>> list() async {
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
      return list;
    } on DioException {
      // Network failure: degrade to local-only so the UI still works.
      return const [localCluster];
    }
  }
}

final clusterRepositoryProvider = Provider<ClusterRepository>((ref) {
  return ClusterRepository(ref.watch(dioProvider));
});

/// Cached cluster list. Picker `ref.refresh(clustersProvider)` to force a
/// fresh fetch on bottom-sheet open.
final clustersProvider = FutureProvider<List<Cluster>>((ref) async {
  return ref.read(clusterRepositoryProvider).list();
});
