// Notification feed widget coverage. Asserts the unread-count
// "Mark all read" button only renders when there are unread items,
// the empty state rows render correctly, and severity-tinted rows show
// for actual notifications. Uses provider overrides to inject a
// pre-built feed page without touching Dio.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/features/notifications_center/feed_repository.dart';
import 'package:kubecenter/features/notifications_center/feed_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

NotificationItem _item({
  required String id,
  String severity = 'warning',
  bool read = false,
  String title = 'Pod restarted',
  String? resourceKind,
  String? resourceName,
}) =>
    NotificationItem(
      id: id,
      source: 'alertmanager',
      severity: severity,
      title: title,
      message: 'Test message body',
      createdAt: DateTime.now().subtract(const Duration(minutes: 3)),
      read: read,
      resourceKind: resourceKind,
      resourceNamespace: resourceKind != null ? 'default' : null,
      resourceName: resourceName,
    );

Widget _harness({
  required NotificationsPage page,
  required int unread,
}) {
  return ProviderScope(
    overrides: [
      notificationsFeedProvider.overrideWith((ref) async => page),
      unreadCountProvider.overrideWith((ref) async => unread),
    ],
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: const NotificationFeedScreen(),
    ),
  );
}

void main() {
  testWidgets('renders empty state when feed has no items', (tester) async {
    await tester.pumpWidget(_harness(
      page: const NotificationsPage(items: [], total: 0),
      unread: 0,
    ));
    await tester.pumpAndSettle();
    expect(find.text('No notifications'), findsOneWidget);
    expect(find.text('Mark all read'), findsNothing);
  });

  testWidgets('renders unread badge + Mark all read action when unread > 0',
      (tester) async {
    await tester.pumpWidget(_harness(
      page: NotificationsPage(
        items: [
          _item(id: 'n-1', severity: 'critical', read: false),
          _item(id: 'n-2', severity: 'warning', read: true),
        ],
        total: 2,
      ),
      unread: 1,
    ));
    await tester.pumpAndSettle();

    // Badge in title shows the count.
    expect(find.text('1'), findsOneWidget);
    // Action button visible because unread > 0.
    expect(find.text('Mark all read'), findsOneWidget);
    // Both rows render their title.
    expect(find.text('Pod restarted'), findsNWidgets(2));
  });

  testWidgets('Mark all read action is hidden when unread == 0',
      (tester) async {
    await tester.pumpWidget(_harness(
      page: NotificationsPage(
        items: [_item(id: 'n-3', read: true)],
        total: 1,
      ),
      unread: 0,
    ));
    await tester.pumpAndSettle();
    expect(find.text('Mark all read'), findsNothing);
  });

  testWidgets('shows truncation footer when total > items.length',
      (tester) async {
    await tester.pumpWidget(_harness(
      page: NotificationsPage(
        items: [
          _item(id: 'n-1', read: true),
          _item(id: 'n-2', read: true),
        ],
        total: 137,
      ),
      unread: 0,
    ));
    await tester.pumpAndSettle();
    expect(find.text('Showing 2 of 137'), findsOneWidget);
  });

  testWidgets('hides truncation footer when total == items.length',
      (tester) async {
    await tester.pumpWidget(_harness(
      page: NotificationsPage(
        items: [
          _item(id: 'n-1', read: true),
          _item(id: 'n-2', read: true),
        ],
        total: 2,
      ),
      unread: 0,
    ));
    await tester.pumpAndSettle();
    expect(find.textContaining('Showing'), findsNothing);
  });

  // Back-navigation contract: the AppBar must always offer a way off the
  // feed. Pushed (drawer) returns to the origin; deep-linked (no back
  // stack) falls back to the dashboard. Mirrors settings_screen_test.
  GoRouter backNavRouter({required String initialLocation}) => GoRouter(
        initialLocation: initialLocation,
        routes: [
          GoRoute(
            path: '/',
            builder: (_, _) => const Scaffold(body: Text('ORIGIN_PAGE')),
          ),
          GoRoute(
            path: '/notifications',
            builder: (_, _) => const NotificationFeedScreen(),
          ),
        ],
      );

  Future<void> pumpRouter(WidgetTester tester, GoRouter router) async {
    addTearDown(router.dispose);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          notificationsFeedProvider
              .overrideWith((ref) async => const NotificationsPage(
                    items: [],
                    total: 0,
                  )),
          unreadCountProvider.overrideWith((ref) async => 0),
        ],
        child: MaterialApp.router(
          theme: buildKubeTheme('nexus'),
          routerConfig: router,
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('AppBar back button returns to the originating screen',
      (tester) async {
    final router = backNavRouter(initialLocation: '/');
    await pumpRouter(tester, router);

    router.push('/notifications');
    await tester.pumpAndSettle();
    expect(find.widgetWithText(AppBar, 'Notifications'), findsOneWidget);

    await tester.tap(find.byType(BackButton));
    await tester.pumpAndSettle();

    expect(find.text('ORIGIN_PAGE'), findsOneWidget);
    expect(find.widgetWithText(AppBar, 'Notifications'), findsNothing);
  });

  testWidgets('AppBar back button falls back to the dashboard with no stack',
      (tester) async {
    final router = backNavRouter(initialLocation: '/notifications');
    await pumpRouter(tester, router);
    expect(find.widgetWithText(AppBar, 'Notifications'), findsOneWidget);

    await tester.tap(find.byType(BackButton));
    await tester.pumpAndSettle();

    expect(find.text('ORIGIN_PAGE'), findsOneWidget);
  });
}
