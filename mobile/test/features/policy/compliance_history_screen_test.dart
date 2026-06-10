// Widget tests for ComplianceHistoryScreen.
//
// Required by the plan as a dedicated 503-distinguished-error path
// regression test. The screen must:
//   * Render the line chart when the backend returns datapoints.
//   * Surface a NON-RETRYABLE empty state when the backend returns 503
//     with body "requires a database" (PostgreSQL not configured).
//   * Surface a RETRY-able error state when the backend returns 503 with
//     any other wording (rolling restart, network issue).
//   * Render an Admin-only empty state when the user is not an admin.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/features/policy/compliance_history_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

class _FakeAuth extends AuthRepository {
  _FakeAuth(this._initial);
  final AuthState _initial;
  @override
  AuthState build() => _initial;
}

Future<void> _pump(
  WidgetTester tester,
  MockDioAdapter mock, {
  bool admin = true,
}) async {
  await tester.binding.setSurfaceSize(const Size(800, 1600));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final user = UserInfo(
    id: 'u1',
    username: admin ? 'admin' : 'viewer',
    provider: 'local',
    roles: admin ? const ['admin'] : const ['viewer'],
  );

  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const ComplianceHistoryScreen(),
      ),
    ],
  );

  await tester.pumpWidget(ProviderScope(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
      authRepositoryProvider.overrideWith(
        () => _FakeAuth(
          AuthAuthenticated(
            user: user,
            rbac: const RBACSummary(),
          ),
        ),
      ),
    ],
    child: _DioInstaller(
      mock: mock,
      child: MaterialApp.router(
        theme: buildKubeTheme('liquid-glass'),
        routerConfig: router,
      ),
    ),
  ));
  await tester.pump();
  await tester.pumpAndSettle(const Duration(milliseconds: 200));
}

class _DioInstaller extends ConsumerWidget {
  const _DioInstaller({required this.mock, required this.child});

  final MockDioAdapter mock;
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    ref.read(dioProvider).httpClientAdapter = mock;
    return child;
  }
}

Map<String, Object?> _detected() => {
      'data': {
        'detected': 'both',
        'kyverno': {'available': true, 'webhooks': 1},
        'gatekeeper': {'available': true, 'webhooks': 1},
      },
    };

void main() {
  testWidgets('renders line chart when datapoints are returned',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detected())
      ..onJson('GET', '/api/v1/policies/compliance/history', body: {
        'data': [
          {
            'date': '2026-05-01',
            'score': 80.0,
            'pass': 8,
            'fail': 2,
            'warn': 0,
            'total': 10,
          },
          {
            'date': '2026-05-02',
            'score': 92.0,
            'pass': 9,
            'fail': 0,
            'warn': 1,
            'total': 10,
          },
        ],
      });
    await _pump(tester, mock);

    expect(find.text('Compliance history'), findsAtLeastNWidgets(1));
    expect(find.text('Last 2 days'), findsOneWidget);
    expect(find.textContaining('Latest: 92%'), findsOneWidget);
    // Latest row table should surface the dates verbatim.
    expect(find.text('2026-05-02'), findsOneWidget);
    expect(find.text('2026-05-01'), findsOneWidget);
  });

  testWidgets(
    '503 with "requires a database" → NON-RETRYABLE empty state '
    '(no Retry button)',
    (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/policies/status', body: _detected())
        ..onJson(
          'GET',
          '/api/v1/policies/compliance/history',
          status: 503,
          body: {
            'error': {
              'code': 503,
              'message': 'compliance history requires a database',
            },
          },
        );
      await _pump(tester, mock);

      // The FeatureUnavailableState wording: the database-not-configured
      // helpMessage MUST appear so operators see the actual remediation
      // (configure PostgreSQL), not the generic install-Kyverno guidance.
      expect(find.text('Compliance history'), findsAtLeastNWidgets(1));
      expect(find.textContaining('database storage'), findsOneWidget);
      expect(find.textContaining('PostgreSQL'), findsOneWidget);

      // The critical contract: NO Retry button. A retry button would
      // deterministically hit 503 again until the operator configures
      // PostgreSQL server-side, which they cannot do from the phone.
      expect(find.widgetWithText(FilledButton, 'Retry'), findsNothing,
          reason:
              'Permanent empty state must not offer Retry — it would '
              'mislead operators about whether the issue is transient.');
      expect(find.widgetWithText(OutlinedButton, 'Retry'), findsNothing);
    },
  );

  testWidgets(
    '503 with non-database wording → RETRY-able error state '
    '(Retry button present)',
    (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/policies/status', body: _detected())
        ..onJson(
          'GET',
          '/api/v1/policies/compliance/history',
          status: 503,
          body: {
            'error': {
              'code': 503,
              'message': 'backend temporarily unreachable',
            },
          },
        );
      await _pump(tester, mock);

      // Generic ErrorStateView — has a Retry button.
      expect(find.text('Retry'), findsOneWidget,
          reason:
              'Generic 503 must stay retry-able — operator should be '
              'able to recover after a rolling restart.');
    },
  );

  testWidgets('non-admin user sees Admin-only empty state', (tester) async {
    final mock = MockDioAdapter();
    // No need for status or history — admin gate fires first.
    await _pump(tester, mock, admin: false);

    expect(find.text('Admin only'), findsOneWidget);
    expect(find.textContaining('cluster administrators'), findsOneWidget);
  });
}
