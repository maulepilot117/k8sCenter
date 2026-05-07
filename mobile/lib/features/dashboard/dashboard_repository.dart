// Fetches the dashboard summary endpoint. Auto-refetches when the active
// cluster changes — the FutureProvider watches activeClusterProvider so
// Riverpod invalidates the cache on switch.
//
// Race-safety: switching clusters mid-fetch must not let the slower
// prior-cluster response overwrite the new cluster's state. We use
// `autoDispose` so a swap disposes the old future, and pass a
// CancelToken disposed on `ref.onDispose` so the in-flight request
// is cancelled instead of completing into a stale state.
//
// The backend dashboard endpoint is local-only (informer cache is not
// available for remote clusters) and returns 400 with a clear message
// when X-Cluster-ID is set to a non-local cluster. We surface that case
// as `DashboardLocalOnlyError` so the UI can render targeted guidance
// instead of a raw 400.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/dio_client.dart';
import '../../cluster/cluster_provider.dart';
import 'dashboard_state.dart';

/// Marker error rendered as a friendly "Local cluster only" message in
/// the dashboard screen. The backend signals this case with HTTP 400 +
/// the literal substring "local cluster" in the message.
class DashboardLocalOnlyError implements Exception {
  const DashboardLocalOnlyError();

  @override
  String toString() =>
      'The dashboard summary is only available for the local cluster.';
}

class DashboardRepository {
  DashboardRepository(this._dio);

  final Dio _dio;

  Future<DashboardSummary> fetchSummary({CancelToken? cancelToken}) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/cluster/dashboard-summary',
        cancelToken: cancelToken,
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
      if (CancelToken.isCancel(e)) rethrow;
      // ErrorMappingInterceptor already wraps as ApiError in `error`.
      final err = e.error;
      final apiError = err is ApiError ? err : ApiError.fromDio(e);
      // Backend returns 400 with "only available for the local cluster"
      // when the dashboard endpoint is hit with a non-local cluster id.
      // Translate that into a typed error so the UI can render guidance.
      if (apiError.statusCode == 400 &&
          apiError.message.toLowerCase().contains('local cluster')) {
        throw const DashboardLocalOnlyError();
      }
      throw apiError;
    }
  }
}

final dashboardRepositoryProvider = Provider<DashboardRepository>((ref) {
  return DashboardRepository(ref.watch(dioProvider));
});

/// FutureProvider that watches activeClusterProvider so Riverpod
/// invalidates and refetches whenever the user picks a different cluster.
/// `autoDispose` ensures the prior fetch is torn down on swap, and the
/// CancelToken released by `ref.onDispose` cancels the in-flight HTTP
/// request so a slow stale response can't overwrite fresh state.
final dashboardSummaryProvider =
    FutureProvider.autoDispose<DashboardSummary>((ref) async {
  // Keep alive briefly so a pull-to-refresh that triggers a rebuild
  // doesn't double-fire the request when the screen unmounts/remounts.
  final link = ref.keepAlive();
  ref.onDispose(link.close);

  // Watch — not read — so this provider rebuilds on cluster change.
  ref.watch(activeClusterProvider);

  final cancelToken = CancelToken();
  ref.onDispose(() {
    if (!cancelToken.isCancelled) {
      cancelToken.cancel('cluster changed or screen disposed');
    }
  });

  return ref.read(dashboardRepositoryProvider).fetchSummary(
        cancelToken: cancelToken,
      );
});
