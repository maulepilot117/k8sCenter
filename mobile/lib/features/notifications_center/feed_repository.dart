// Notification feed repository over the existing /v1/notifications API.
// Mirrors the web frontend's NotificationCenterDrawer data flow:
//   - GET /notifications — list (RBAC-filtered server-side)
//   - GET /notifications/unread-count — badge value
//   - POST /notifications/:id/read — single mark-read
//   - POST /notifications/read-all — mark every unread item read
//
// All requests carry the active cluster id via ClusterInterceptor and
// the auth token via AuthInterceptor (per dio_client.dart). Switching
// clusters auto-invalidates feedProvider via activeClusterProvider.

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/dio_client.dart';
import '../../cluster/cluster_provider.dart';

class NotificationItem {
  const NotificationItem({
    required this.id,
    required this.source,
    required this.severity,
    required this.title,
    required this.message,
    required this.createdAt,
    required this.read,
    this.resourceKind,
    this.resourceNamespace,
    this.resourceName,
    this.clusterId,
  });

  factory NotificationItem.fromJson(Map<String, dynamic> j) => NotificationItem(
        id: j['id'] as String? ?? '',
        source: j['source'] as String? ?? '',
        severity: j['severity'] as String? ?? 'info',
        title: j['title'] as String? ?? '',
        message: j['message'] as String? ?? '',
        createdAt: DateTime.tryParse(j['createdAt'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
        read: j['read'] as bool? ?? false,
        resourceKind: j['resourceKind'] as String?,
        resourceNamespace: j['resourceNamespace'] as String?,
        resourceName: j['resourceName'] as String?,
        clusterId: j['clusterId'] as String?,
      );

  final String id;
  final String source;
  final String severity;
  final String title;
  final String message;
  final DateTime createdAt;
  final bool read;

  /// When all three fields are present, the row is deep-linkable to a
  /// resource detail screen. Cluster-scoped notifications carry an
  /// empty resourceNamespace.
  final String? resourceKind;
  final String? resourceNamespace;
  final String? resourceName;
  final String? clusterId;

  bool get hasResourceTarget =>
      resourceKind != null &&
      resourceKind!.isNotEmpty &&
      resourceName != null &&
      resourceName!.isNotEmpty;
}

class NotificationsPage {
  const NotificationsPage({required this.items, required this.total});
  final List<NotificationItem> items;
  final int total;
}

class NotificationsRepository {
  NotificationsRepository(this._dio);
  final Dio _dio;

  Future<NotificationsPage> list({
    String? source,
    String? severity,
    String? read,
    int limit = 50,
  }) async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/notifications',
        queryParameters: {
          if (source != null && source.isNotEmpty) 'source': source,
          if (severity != null && severity.isNotEmpty) 'severity': severity,
          if (read != null && read.isNotEmpty) 'read': read,
          'limit': '$limit',
        },
      );
      final raw = res.data?['data'];
      final list = (raw is List ? raw : const <dynamic>[])
          .whereType<Map<dynamic, dynamic>>()
          .map<NotificationItem>(
            (m) => NotificationItem.fromJson(Map<String, dynamic>.from(m)),
          )
          .toList();
      final meta = res.data?['metadata'] as Map<String, dynamic>? ?? const {};
      final total = (meta['total'] as num?)?.toInt() ?? list.length;
      return NotificationsPage(items: list, total: total);
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  Future<int> unreadCount() async {
    try {
      final res = await _dio.get<Map<String, dynamic>>(
        '/api/v1/notifications/unread-count',
      );
      final data = res.data?['data'] as Map<String, dynamic>? ?? const {};
      return (data['count'] as num?)?.toInt() ?? 0;
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  Future<void> markRead(String id) async {
    try {
      await _dio.post<dynamic>(
        '/api/v1/notifications/${Uri.encodeComponent(id)}/read',
      );
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  Future<void> markAllRead() async {
    try {
      await _dio.post<dynamic>('/api/v1/notifications/read-all');
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }
}

final notificationsRepositoryProvider =
    Provider<NotificationsRepository>((ref) {
  return NotificationsRepository(ref.watch(dioProvider));
});

/// Feed list. Watches activeClusterProvider so cluster switches refetch.
final notificationsFeedProvider =
    FutureProvider.autoDispose<NotificationsPage>((ref) async {
  ref.watch(activeClusterProvider);
  return ref.read(notificationsRepositoryProvider).list();
});

/// Drawer-badge count. Polled lazily; ref.invalidate after read/dispatch.
final unreadCountProvider = FutureProvider.autoDispose<int>((ref) async {
  ref.watch(activeClusterProvider);
  return ref.read(notificationsRepositoryProvider).unreadCount();
});
