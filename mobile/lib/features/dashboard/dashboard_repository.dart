// Fetches the dashboard summary endpoint. Auto-refetches when the active
// cluster changes — the FutureProvider watches activeClusterProvider so
// Riverpod invalidates the cache on switch.
//
// Note: the backend currently returns 400 on the dashboard endpoint for
// non-local clusters (informer cache is local-only). The repository
// surfaces that as ApiError so the UI can render a clear message.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/dio_client.dart';
import '../../cluster/cluster_provider.dart';
import 'dashboard_state.dart';

class DashboardRepository {
  DashboardRepository(this._dio);

  final Dio _dio;

  Future<DashboardSummary> fetchSummary() async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/cluster/dashboard-summary',
      );
      final data = res.data?['data'] as Map<String, dynamic>?;
      if (data == null) {
        throw ApiError(
          statusCode: 500,
          code: 500,
          message: 'dashboard response missing data',
        );
      }
      return DashboardSummary.fromJson(data);
    } on DioException catch (e) {
      // ErrorMappingInterceptor already wraps as ApiError in `error`.
      final err = e.error;
      if (err is ApiError) throw err;
      throw ApiError.fromDio(e);
    }
  }
}

final dashboardRepositoryProvider = Provider<DashboardRepository>((ref) {
  return DashboardRepository(ref.watch(dioProvider));
});

/// FutureProvider that watches activeClusterProvider so Riverpod
/// invalidates and refetches whenever the user picks a different cluster.
final dashboardSummaryProvider =
    FutureProvider<DashboardSummary>((ref) async {
  // Watch — not read — so this provider rebuilds on cluster change.
  ref.watch(activeClusterProvider);
  return ref.read(dashboardRepositoryProvider).fetchSummary();
});
