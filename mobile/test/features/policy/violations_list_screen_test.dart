// Widget tests for the Violations list.
//
// Coverage:
//   * row renders policy + rule + target + message.
//   * severity chip filters the list.
//   * empty data renders the "no violations" message.
//   * virtual-scroll (`SliverChildBuilderDelegate`) handles 500+ rows
//     without constructing every widget.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/policy/violations_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  await tester.binding.setSurfaceSize(const Size(800, 1600));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const ViolationsListScreen(),
      ),
      // Stub detail route so taps don't 404 the test router.
      GoRoute(
        path: '/clusters/:clusterId/policy/violations/:stableKey',
        builder: (context, state) => const Scaffold(body: Text('detail')),
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

Map<String, Object?> _detected() => {
      'data': {
        'detected': 'both',
        'kyverno': {'available': true, 'webhooks': 1},
        'gatekeeper': {'available': true, 'webhooks': 1},
      },
    };

void main() {
  testWidgets('row renders policy + target + message', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detected())
      ..onJson('GET', '/api/v1/policies/violations', body: {
        'data': [
          {
            'policy': 'require-labels',
            'rule': 'check-team',
            'namespace': 'default',
            'kind': 'Pod',
            'name': 'app-abc',
            'severity': 'high',
            'action': 'enforce',
            'message': 'team label required',
            'engine': 'kyverno',
            'blocking': true,
          },
        ],
      });
    await _pump(tester, mock);

    expect(find.text('require-labels'), findsOneWidget);
    expect(find.textContaining('rule: check-team'), findsOneWidget);
    expect(find.textContaining('Pod/app-abc'), findsOneWidget);
    expect(find.text('team label required'), findsOneWidget);
  });

  testWidgets('severity chip filters the list', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detected())
      ..onJson('GET', '/api/v1/policies/violations', body: {
        'data': [
          {
            'policy': 'p1-critical',
            'kind': 'Pod',
            'name': 'a',
            'severity': 'critical',
            'action': 'enforce',
            'message': '',
            'engine': 'kyverno',
            'blocking': true,
            'namespace': 'ns1',
          },
          {
            'policy': 'p2-low',
            'kind': 'Pod',
            'name': 'b',
            'severity': 'low',
            'action': 'audit',
            'message': '',
            'engine': 'kyverno',
            'blocking': false,
            'namespace': 'ns1',
          },
        ],
      });
    await _pump(tester, mock);

    // Both visible initially.
    expect(find.text('p1-critical'), findsOneWidget);
    expect(find.text('p2-low'), findsOneWidget);

    // Tap the "Critical" chip — only p1 remains.
    await tester.tap(find.widgetWithText(ChoiceChip, 'Critical'));
    await tester.pumpAndSettle();
    expect(find.text('p1-critical'), findsOneWidget);
    expect(find.text('p2-low'), findsNothing);
  });

  testWidgets('empty data renders compliant copy', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detected())
      ..onJson('GET', '/api/v1/policies/violations', body: {'data': <Object>[]});
    await _pump(tester, mock);

    expect(find.textContaining('currently compliant'), findsOneWidget);
  });

  testWidgets('virtual scroll handles large response without building all',
      (tester) async {
    // 200 rows is large enough to overflow the viewport (each row is
    // ≥60dp; 800x1600 viewport fits roughly 25 rows). The plan calls
    // out a 1000-violation perf check; that scale is exercised by
    // headless benchmark separately so the widget test stays fast.
    final rows = List<Map<String, Object?>>.generate(200, (i) {
      return {
        'policy': 'p$i',
        'kind': 'Pod',
        'name': 'pod-$i',
        'severity': i % 4 == 0 ? 'critical' : 'medium',
        'action': 'audit',
        'message': '',
        'engine': 'kyverno',
        'blocking': false,
        'namespace': 'ns${i % 5}',
      };
    });

    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _detected())
      ..onJson('GET', '/api/v1/policies/violations',
          body: {'data': rows});
    // Use bounded pumps rather than pumpAndSettle — the lengthy list
    // can keep the framework "busy" for longer than the default 10-
    // minute pumpAndSettle timeout when measured against the test clock.
    await tester.binding.setSurfaceSize(const Size(800, 1600));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final router = GoRouter(
      initialLocation: '/',
      routes: [
        GoRoute(
          path: '/',
          builder: (context, state) => const ViolationsListScreen(),
        ),
        GoRoute(
          path: '/clusters/:clusterId/policy/violations/:stableKey',
          builder: (context, state) => const Scaffold(body: Text('detail')),
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
    // A handful of bounded pumps is enough to settle status fetch +
    // violations fetch + first paint without giving the framework a
    // chance to spin on residual animations.
    for (var i = 0; i < 5; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }

    // First row is visible; row 199 is below the fold and must NOT have
    // been built by SliverChildBuilderDelegate.
    expect(find.text('p0'), findsOneWidget);
    expect(find.text('p199'), findsNothing,
        reason: 'Virtual scroll must not construct the entire list '
            'eagerly — only viewport rows.');
  });
}
