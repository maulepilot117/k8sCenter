// Redirect guard contract for the onboarding + auth branches added in
// M5 PR-5g. Three cases:
//
//   1. Not authenticated + onboarded=false  → /onboarding
//   2. Not authenticated + onboarded=true   → /login
//   3. Authenticated + atOnboarding         → /  (dashboard)
//
// Tests pump a minimal widget tree wired with ProviderScope overrides
// so they exercise the real GoRouter redirect logic without spinning up
// actual platform channels or network sockets.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/features/onboarding/onboarding_controller.dart';
import 'package:kubecenter/providers/shared_preferences_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

// ---------------------------------------------------------------------------
// Stub screens so the widget tree can render without pulling in real screens.
// ---------------------------------------------------------------------------
class _OnboardingStub extends StatelessWidget {
  const _OnboardingStub();
  @override
  Widget build(BuildContext context) =>
      const Scaffold(body: Center(child: Text('ONBOARDING')));
}

class _LoginStub extends StatelessWidget {
  const _LoginStub();
  @override
  Widget build(BuildContext context) =>
      const Scaffold(body: Center(child: Text('LOGIN')));
}

class _DashboardStub extends StatelessWidget {
  const _DashboardStub();
  @override
  Widget build(BuildContext context) =>
      const Scaffold(body: Center(child: Text('DASHBOARD')));
}

// ---------------------------------------------------------------------------
// Fake AuthRepository that seeds a fixed initial state.
// ---------------------------------------------------------------------------
class _FakeAuth extends AuthRepository {
  _FakeAuth(this._initial);
  final AuthState _initial;
  @override
  AuthState build() => _initial;
}

// ---------------------------------------------------------------------------
// Minimal router that mirrors the redirect logic in app_router.dart but
// uses stub screens so tests don't need platform plugin registrations.
// ---------------------------------------------------------------------------
GoRouter _buildRouter(WidgetRef ref) {
  final authState = ref.watch(authRepositoryProvider);
  return GoRouter(
    initialLocation: '/onboarding',
    refreshListenable: _AuthListenable(ref),
    redirect: (context, state) {
      final loggedIn = authState is AuthAuthenticated;
      final initializing = authState is AuthInitializing;
      final atLogin = state.matchedLocation == '/login';
      final atOnboarding = state.matchedLocation == '/onboarding';

      if (initializing) return null;

      if (!loggedIn) {
        final onboarded = ref.read(onboardingControllerProvider);
        if (!onboarded) return atOnboarding ? null : '/onboarding';
        return atLogin ? null : '/login';
      }

      if (atLogin || atOnboarding) return '/';
      return null;
    },
    routes: [
      GoRoute(
        path: '/onboarding',
        builder: (_, s) => const _OnboardingStub(),
      ),
      GoRoute(
        path: '/login',
        builder: (_, s) => const _LoginStub(),
      ),
      GoRoute(
        path: '/',
        builder: (_, s) => const _DashboardStub(),
      ),
    ],
  );
}

class _AuthListenable extends ChangeNotifier {
  _AuthListenable(this._ref) {
    // WidgetRef.listen returns void and auto-disposes with the widget.
    _ref.listen<AuthState>(
      authRepositoryProvider,
      (prev, next) { notifyListeners(); },
    );
  }
  final WidgetRef _ref;
  // No _sub field: WidgetRef.listen does not return a ProviderSubscription.
}

// ---------------------------------------------------------------------------
// Helper: build a ProviderScope + router with overrides and pump it.
// ---------------------------------------------------------------------------
Future<void> _pumpRouter(
  WidgetTester tester, {
  required AuthState authState,
  required bool onboarded,
}) async {
  SharedPreferences.setMockInitialValues(
    onboarded ? {kOnboardedPrefsKey: true} : {},
  );
  final prefs = await SharedPreferences.getInstance();

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        sharedPreferencesProvider.overrideWithValue(prefs),
        authRepositoryProvider.overrideWith(() => _FakeAuth(authState)),
      ],
      child: Consumer(
        builder: (context, ref, child) => MaterialApp.router(
          routerConfig: _buildRouter(ref),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------
void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets(
    '1. not authenticated + onboarded=false → redirects to /onboarding',
    (tester) async {
      await _pumpRouter(
        tester,
        authState: const AuthUnauthenticated(),
        onboarded: false,
      );
      expect(find.text('ONBOARDING'), findsOneWidget);
      expect(find.text('LOGIN'), findsNothing);
      expect(find.text('DASHBOARD'), findsNothing);
    },
  );

  testWidgets(
    '2. not authenticated + onboarded=true → redirects to /login',
    (tester) async {
      await _pumpRouter(
        tester,
        authState: const AuthUnauthenticated(),
        onboarded: true,
      );
      expect(find.text('LOGIN'), findsOneWidget);
      expect(find.text('ONBOARDING'), findsNothing);
    },
  );

  testWidgets(
    '3. authenticated + atOnboarding → redirects to /',
    (tester) async {
      await _pumpRouter(
        tester,
        authState: AuthAuthenticated(
          user: const UserInfo(
            id: 'u1',
            username: 'admin',
            provider: 'local',
            roles: ['admin'],
          ),
          rbac: const RBACSummary(),
        ),
        onboarded: false, // flag value doesn't matter when authenticated
      );
      expect(find.text('DASHBOARD'), findsOneWidget);
      expect(find.text('ONBOARDING'), findsNothing);
      expect(find.text('LOGIN'), findsNothing);
    },
  );
}
