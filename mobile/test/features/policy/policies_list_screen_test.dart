// Widget tests for the Policies list.
//
// Coverage:
//   * row renders name, engine/severity/blocking badges, violation count.
//   * engine-availability badge — Kyverno policy on Gatekeeper-only cluster
//     surfaces the "Engine not installed" tooltip (plan §U9 line 640).
//   * engine filter chip narrows the list.
//   * severity filter chip narrows the list.
//   * blocking filter chip narrows the list.
//   * free-text search narrows the list.
//   * empty-data copy and no-matches copy.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/policy/policies_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  // 1600dp wide so the horizontal chip scroll-view fits every chip on
  // screen (3 engine + 5 severity + 3 blocking ≈ 11 chips at ~80dp each
  // including spacing — narrower viewports push the later chips off the
  // right edge where `tester.tap` cannot reach them without a scroll
  // gesture).
  await tester.binding.setSurfaceSize(const Size(1600, 1600));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const PoliciesListScreen(),
      ),
    ],
  );

  await tester.pumpWidget(ProviderScope(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
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
        'kyverno': {'available': true, 'webhooks': 1},
        'gatekeeper': {'available': true, 'webhooks': 1},
      },
    };

Map<String, Object?> _detectedGatekeeperOnly() => {
      'data': {
        'detected': 'gatekeeper',
        'kyverno': {'available': false, 'webhooks': 0},
        'gatekeeper': {'available': true, 'webhooks': 1},
      },
    };

Map<String, Object?> _mixedPolicies() => {
      'data': [
        {
          'id': 'kyverno::require-labels',
          'name': 'require-labels',
          'kind': 'ClusterPolicy',
          'action': 'enforce',
          'severity': 'critical',
          'engine': 'kyverno',
          'blocking': true,
          'ready': true,
          'ruleCount': 1,
          'violationCount': 4,
          'description': 'team label must be present',
        },
        {
          'id': 'kyverno::audit-priv',
          'name': 'audit-priv',
          'kind': 'ClusterPolicy',
          'action': 'audit',
          'severity': 'medium',
          'engine': 'kyverno',
          'blocking': false,
          'ready': true,
          'ruleCount': 1,
          'violationCount': 0,
        },
        {
          'id': 'gatekeeper::K8sRequiredLabels/team',
          'name': 'team-label-constraint',
          'kind': 'K8sRequiredLabels',
          'action': 'deny',
          'severity': 'high',
          'engine': 'gatekeeper',
          'blocking': true,
          'ready': true,
          'ruleCount': 1,
          'violationCount': 2,
        },
      ],
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

  testWidgets('row renders name + badges + violation count', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _mixedPolicies());
    await _pump(tester, mock);

    expect(find.text('require-labels'), findsOneWidget);
    expect(find.text('audit-priv'), findsOneWidget);
    expect(find.text('team-label-constraint'), findsOneWidget);
    // Severity + engine pills render in the row.
    expect(find.text('Critical'), findsAtLeastNWidgets(1));
    expect(find.text('Kyverno'), findsAtLeastNWidgets(1));
    expect(find.text('Gatekeeper'), findsAtLeastNWidgets(1));
    // Violation counts surface as ergonomic copy on rows with > 0.
    expect(find.textContaining('4 violations'), findsOneWidget);
    expect(find.textContaining('2 violations'), findsOneWidget);
    // Description renders when present.
    expect(find.text('team label must be present'), findsOneWidget);
  });

  testWidgets(
    'Kyverno-engined policy on Gatekeeper-only cluster surfaces the '
    'Engine not installed tooltip (plan §U9 line 640)',
    (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/policies/status',
            body: _detectedGatekeeperOnly())
        ..onJson('GET', '/api/v1/policies/', body: _mixedPolicies());
      await _pump(tester, mock);

      // Long-press surfaces the Tooltip message; locate Tooltip widgets
      // by their message text since the Tooltip child contains the
      // EngineBadge but the message is what tells the operator the
      // engine isn't installed.
      final tooltips = find.byWidgetPredicate((w) {
        return w is Tooltip &&
            w.message == 'Engine not installed on this cluster';
      });
      expect(
        tooltips,
        findsAtLeastNWidgets(2),
        reason:
            'Both Kyverno-engined policies (require-labels, audit-priv) '
            'must carry the install-state tooltip on a Gatekeeper-only '
            'cluster; the Gatekeeper-engined row must not.',
      );
    },
  );

  testWidgets('engine filter chip narrows the list', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _mixedPolicies());
    await _pump(tester, mock);

    expect(find.text('require-labels'), findsOneWidget);
    expect(find.text('team-label-constraint'), findsOneWidget);

    await tester.tap(find.widgetWithText(ChoiceChip, 'Kyverno'));
    await tester.pumpAndSettle();

    expect(find.text('require-labels'), findsOneWidget);
    expect(find.text('audit-priv'), findsOneWidget);
    expect(find.text('team-label-constraint'), findsNothing,
        reason: 'Gatekeeper-engined policy must drop out when Kyverno '
            'chip is selected.');
  });

  testWidgets('severity filter chip narrows the list', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _mixedPolicies());
    await _pump(tester, mock);

    await tester.tap(find.widgetWithText(ChoiceChip, 'Critical'));
    await tester.pumpAndSettle();

    expect(find.text('require-labels'), findsOneWidget);
    expect(find.text('audit-priv'), findsNothing);
    expect(find.text('team-label-constraint'), findsNothing);
  });

  testWidgets('blocking filter chip narrows the list', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _mixedPolicies());
    await _pump(tester, mock);

    await tester.tap(find.widgetWithText(ChoiceChip, 'Audit'));
    await tester.pumpAndSettle();

    // Only audit-priv is non-blocking.
    expect(find.text('audit-priv'), findsOneWidget);
    expect(find.text('require-labels'), findsNothing);
    expect(find.text('team-label-constraint'), findsNothing);
  });

  testWidgets('free-text search narrows the list', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _mixedPolicies());
    await _pump(tester, mock);

    await tester.enterText(find.byType(TextField), 'audit');
    await tester.pumpAndSettle();

    expect(find.text('audit-priv'), findsOneWidget);
    expect(find.text('require-labels'), findsNothing);
    expect(find.text('team-label-constraint'), findsNothing);
  });

  testWidgets('empty data renders compliance-friendly copy', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: {'data': <Object>[]});
    await _pump(tester, mock);

    expect(find.textContaining('No policies defined'), findsOneWidget);
  });

  testWidgets('non-empty data with no chip matches shows no-matches copy',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detectedBoth())
      ..onJson('GET', '/api/v1/policies/', body: _mixedPolicies());
    await _pump(tester, mock);

    // Filter to a severity that has zero matches in the fixture
    // (low severity not present in _mixedPolicies).
    await tester.tap(find.widgetWithText(ChoiceChip, 'Low'));
    await tester.pumpAndSettle();

    expect(find.textContaining('No policies match'), findsOneWidget);
  });
}
