// Widget tests for the Policy dashboard.
//
// Coverage:
//   * detected=false → FeatureUnavailableState.policy().
//   * compliance score + tier label render.
//   * engine cards render install state + webhook count.
//   * severity breakdown renders ordered desc.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/features/policy/dashboard_screen.dart';
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
        builder: (context, state) => const PolicyDashboardScreen(),
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
        theme: buildKubeTheme('nexus'),
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

Map<String, Object?> _detectedBoth() => {
      'data': {
        'detected': 'both',
        'kyverno': {
          'available': true,
          'namespace': 'kyverno',
          'webhooks': 3,
        },
        'gatekeeper': {
          'available': true,
          'namespace': 'gatekeeper-system',
          'webhooks': 2,
        },
        'lastChecked': '2026-05-12T10:00:00Z',
      },
    };

Map<String, Object?> _detectedKyvernoOnly() => {
      'data': {
        'detected': 'kyverno',
        'kyverno': {
          'available': true,
          'namespace': 'kyverno',
          'webhooks': 3,
        },
        'gatekeeper': {'available': false, 'webhooks': 0},
      },
    };

Map<String, Object?> _samplePolicies() => {
      'data': [
        {
          'id': 'kyverno::ClusterPolicy:require-labels',
          'name': 'require-labels',
          'kind': 'ClusterPolicy',
          'action': 'enforce',
          'severity': 'high',
          'engine': 'kyverno',
          'blocking': true,
          'ready': true,
          'ruleCount': 1,
          'violationCount': 0,
        },
        {
          'id': 'kyverno::ClusterPolicy:require-team',
          'name': 'require-team',
          'kind': 'ClusterPolicy',
          'action': 'audit',
          'severity': 'medium',
          'engine': 'kyverno',
          'blocking': false,
          'ready': true,
          'ruleCount': 1,
          'violationCount': 2,
        },
      ],
    };

Map<String, Object?> _sampleCompliance() => {
      'data': {
        'scope': '',
        'score': 87.5,
        'pass': 14,
        'fail': 2,
        'warn': 0,
        'total': 16,
        'bySeverity': {
          'critical': {'pass': 2, 'fail': 0, 'total': 2},
          'high': {'pass': 5, 'fail': 1, 'total': 6},
          'medium': {'pass': 5, 'fail': 1, 'total': 6},
          'low': {'pass': 2, 'fail': 0, 'total': 2},
        },
      },
    };

void main() {
  testWidgets('detected=false renders policy not-installed state',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: {
        'data': {'detected': ''},
      });
    await _pump(tester, mock);

    expect(find.textContaining('policy engine'), findsOneWidget);
    expect(find.textContaining('is not installed'), findsOneWidget);
  });

  testWidgets('renders compliance score with tier label', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _samplePolicies())
      ..onJson('GET', '/api/v1/policies/violations', body: {'data': <Object>[]})
      ..onJson('GET', '/api/v1/policies/compliance',
          body: _sampleCompliance());
    await _pump(tester, mock);

    // 87% is below the healthy threshold (90) but above at-risk (70).
    expect(find.text('88%'), findsOneWidget,
        reason: 'gauge label is rounded to nearest int — 87.5 → 88.');
    expect(find.text('At risk'), findsAtLeastNWidgets(1));
    expect(find.textContaining('14/16'), findsOneWidget);
  });

  testWidgets('engine cards show install state + webhook counts',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _samplePolicies())
      ..onJson('GET', '/api/v1/policies/violations', body: {'data': <Object>[]})
      ..onJson('GET', '/api/v1/policies/compliance',
          body: _sampleCompliance());
    await _pump(tester, mock);

    expect(find.text('Kyverno'), findsAtLeastNWidgets(1));
    expect(find.text('Gatekeeper'), findsAtLeastNWidgets(1));
    expect(find.text('Installed'), findsNWidgets(2));
    expect(find.textContaining('3 webhooks'), findsOneWidget);
    expect(find.textContaining('2 webhooks'), findsOneWidget);
  });

  testWidgets('gatekeeper-only cluster shows Gatekeeper as Not installed',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status',
          body: _detectedKyvernoOnly())
      ..onJson('GET', '/api/v1/policies/', body: _samplePolicies())
      ..onJson('GET', '/api/v1/policies/violations', body: {'data': <Object>[]})
      ..onJson('GET', '/api/v1/policies/compliance',
          body: _sampleCompliance());
    await _pump(tester, mock);

    expect(find.text('Not installed'), findsOneWidget,
        reason: 'Gatekeeper card surfaces install state when absent.');
  });

  testWidgets('severity breakdown renders ordered desc', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _samplePolicies())
      ..onJson('GET', '/api/v1/policies/violations', body: {'data': <Object>[]})
      ..onJson('GET', '/api/v1/policies/compliance',
          body: _sampleCompliance());
    await _pump(tester, mock);

    final critical = find.text('Critical');
    final high = find.text('High');
    final medium = find.text('Medium');
    final low = find.text('Low');
    expect(critical, findsOneWidget);
    expect(high, findsOneWidget);
    expect(medium, findsOneWidget);
    expect(low, findsOneWidget);

    // Critical must appear above Low in the layout — y-coordinate check
    // confirms the desc ordering matches the policy weight table.
    final criticalY = tester.getTopLeft(critical).dy;
    final lowY = tester.getTopLeft(low).dy;
    expect(criticalY < lowY, isTrue,
        reason: 'Critical row must render above Low.');
  });

  testWidgets('non-admin hides the compliance-history browse tile',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _samplePolicies())
      ..onJson('GET', '/api/v1/policies/violations', body: {'data': <Object>[]})
      ..onJson('GET', '/api/v1/policies/compliance',
          body: _sampleCompliance());
    await _pump(tester, mock, admin: false);

    // History tile must not appear; Policies + Violations remain.
    expect(find.text('Policies'), findsAtLeastNWidgets(1));
    expect(find.text('Violations'), findsOneWidget);
    expect(find.text('History'), findsNothing,
        reason: 'Non-admin operators must not see a tile they cannot use.');
  });
}
