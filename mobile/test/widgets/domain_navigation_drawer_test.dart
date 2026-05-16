// Verifies the navigation-drawer's Settings tile navigates to /settings.
//
// Builds a minimal MaterialApp+GoRouter harness with the drawer mounted
// in a Scaffold. Auth state is forced to AuthAuthenticated so the
// drawer's submenu / RBAC paths don't short-circuit. The unread badge
// provider is overridden to AsyncData(0) so we don't hit the network.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/features/notifications_center/feed_repository.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/domain_navigation_drawer.dart';

class _FakeAuth extends AuthRepository {
  _FakeAuth(this._initial);
  final AuthState _initial;
  @override
  AuthState build() => _initial;
}

void main() {
  testWidgets('tapping drawer-settings navigates to /settings',
      (tester) async {
    final router = GoRouter(
      initialLocation: '/',
      routes: [
        GoRoute(
          path: '/',
          builder: (context, state) => const Scaffold(
            drawer: DomainNavigationDrawer(),
            body: SizedBox.expand(),
          ),
        ),
        GoRoute(
          path: '/settings',
          builder: (context, state) => const Scaffold(body: Text('SETTINGS_PAGE')),
        ),
      ],
    );
    addTearDown(router.dispose);

    final user = UserInfo(
      id: 'u1',
      username: 'admin',
      provider: 'local',
      roles: const ['admin'],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authRepositoryProvider.overrideWith(
            () => _FakeAuth(
              AuthAuthenticated(
                user: user,
                rbac: RBACSummary.fromJson(<String, dynamic>{}),
              ),
            ),
          ),
          // No badge — keeps the drawer from hitting the network.
          unreadCountProvider.overrideWith((ref) async => 0),
        ],
        child: MaterialApp.router(
          theme: buildKubeTheme('nexus'),
          routerConfig: router,
        ),
      ),
    );
    await tester.pumpAndSettle();

    // Open the drawer.
    final scaffoldState =
        tester.state<ScaffoldState>(find.byType(Scaffold).first);
    scaffoldState.openDrawer();
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('drawer-settings')));
    await tester.pumpAndSettle();

    expect(find.text('SETTINGS_PAGE'), findsOneWidget);
  });
}
