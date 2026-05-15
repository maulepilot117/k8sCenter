// Widget tests for the Violation detail screen.
//
// The screen has no GET-by-id endpoint — it reads from the cached
// violationsListProvider and matches by stable key
// `policy|rule|namespace|kind|name`. Coverage:
//   * stable-key found → _DetailContent renders with target + remediation
//   * stable-key not found → "Violation not found" empty state
//   * cluster-scoped resource: target-resource path uses the namespace
//     sentinel (`_`) so generic-detail routing resolves cleanly
//   * remediation hint copy varies by blocking + engine

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/policy/violation_detail_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

class _NavObserver extends NavigatorObserver {
  final List<Route<dynamic>> pushed = [];

  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previousRoute) {
    pushed.add(route);
    super.didPush(route, previousRoute);
  }
}

Future<({_NavObserver observer, GoRouter router})> _pump(
  WidgetTester tester,
  MockDioAdapter mock, {
  required String stableKey,
}) async {
  await tester.binding.setSurfaceSize(const Size(800, 1600));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final observer = _NavObserver();
  final router = GoRouter(
    initialLocation: '/',
    observers: [observer],
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) =>
            ViolationDetailScreen(stableKey: stableKey),
      ),
      // Stub the generic-detail catch-all so the "View target resource"
      // button has somewhere to land. Capture the path-parameters via
      // the observer to assert against.
      GoRoute(
        path: '/clusters/:clusterId/generic/:kind/:namespace/:name',
        builder: (context, state) => Scaffold(
          body: Text(
            'generic:${state.pathParameters['kind']}:'
            '${state.pathParameters['namespace']}:'
            '${state.pathParameters['name']}',
          ),
        ),
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

  return (observer: observer, router: router);
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

Map<String, Object?> _violations() => {
      'data': [
        {
          'policy': 'require-labels',
          'rule': 'check-team',
          'namespace': 'app',
          'kind': 'Pod',
          'name': 'web-1',
          'severity': 'high',
          'action': 'enforce',
          'message': 'team label required',
          'engine': 'kyverno',
          'blocking': true,
        },
        {
          'policy': 'audit-priv',
          'rule': '',
          'namespace': 'app',
          'kind': 'Pod',
          'name': 'sidecar-2',
          'severity': 'medium',
          'action': 'audit',
          'message': 'privilege flagged',
          'engine': 'kyverno',
          'blocking': false,
        },
        {
          'policy': 'K8sRequiredLabels/team',
          'namespace': '',
          'kind': 'Namespace',
          'name': 'demo',
          'severity': 'low',
          'action': 'dryrun',
          'message': 'namespace missing label',
          'engine': 'gatekeeper',
          'blocking': false,
        },
      ],
    };

void main() {
  testWidgets(
    'stableKey found → renders policy + target + remediation sections',
    (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/policies/status', body: _detected())
        ..onJson('GET', '/api/v1/policies/violations', body: _violations());
      await _pump(
        tester,
        mock,
        stableKey: 'require-labels|check-team|app|Pod|web-1',
      );

      expect(find.text('require-labels'), findsOneWidget);
      expect(find.textContaining('Rule'), findsOneWidget);
      expect(find.text('check-team'), findsOneWidget);
      expect(find.text('team label required'), findsOneWidget);
      // Target resource section + labels.
      expect(find.text('Target resource'), findsOneWidget);
      expect(find.text('Pod'), findsOneWidget);
      expect(find.text('app'), findsOneWidget);
      expect(find.text('web-1'), findsOneWidget);
      // Remediation section.
      expect(find.text('Remediation'), findsOneWidget);
    },
  );

  testWidgets(
    'stableKey absent from list → "Violation not found" empty state',
    (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/policies/status', body: _detected())
        ..onJson('GET', '/api/v1/policies/violations', body: _violations());
      await _pump(
        tester,
        mock,
        stableKey: 'phantom-policy||Namespace|missing',
      );

      expect(find.text('Violation not found'), findsOneWidget);
      expect(
        find.textContaining('may have been remediated'),
        findsOneWidget,
        reason:
            'Operator should learn the most-likely cause (remediated since '
            'the list was cached) rather than a generic error.',
      );
    },
  );

  testWidgets(
    'cluster-scoped resource: View-target navigates to generic detail '
    'with the namespace sentinel "_"',
    (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/policies/status', body: _detected())
        ..onJson('GET', '/api/v1/policies/violations', body: _violations());
      await _pump(
        tester,
        mock,
        stableKey: 'K8sRequiredLabels/team|||Namespace|demo',
      );

      // Tap the "View target resource" FilledButton.
      await tester.tap(find.widgetWithText(FilledButton, 'View target resource'));
      await tester.pumpAndSettle();

      // The generic-detail stub renders "generic:<kind>:<ns>:<name>".
      // Namespace sentinel "_" must appear because the violation is
      // cluster-scoped (empty namespace on wire).
      expect(find.text('generic:Namespace:_:demo'), findsOneWidget);
    },
  );

  testWidgets(
    'namespaced resource: View-target uses real namespace, not sentinel',
    (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/policies/status', body: _detected())
        ..onJson('GET', '/api/v1/policies/violations', body: _violations());
      await _pump(
        tester,
        mock,
        stableKey: 'require-labels|check-team|app|Pod|web-1',
      );

      await tester.tap(find.widgetWithText(FilledButton, 'View target resource'));
      await tester.pumpAndSettle();

      expect(find.text('generic:Pod:app:web-1'), findsOneWidget);
    },
  );

  testWidgets(
    'remediation copy differentiates blocking vs Kyverno-audit vs '
    'Gatekeeper-audit',
    (tester) async {
      // Three sub-cases, all reading the same mocked violation list but
      // with different stable keys.
      Future<void> testCopyAt(
        String stableKey,
        String expectedFragment,
      ) async {
        final mock = MockDioAdapter()
          ..onJson('GET', '/api/v1/policies/status', body: _detected())
          ..onJson('GET', '/api/v1/policies/violations',
              body: _violations());
        await _pump(tester, mock, stableKey: stableKey);

        expect(
          find.textContaining(expectedFragment),
          findsOneWidget,
          reason:
              'Remediation copy must surface the engine-/blocking-'
              'specific guidance.',
        );
      }

      await testCopyAt(
        'require-labels|check-team|app|Pod|web-1',
        'blocking violation',
      );
      await testCopyAt(
        'audit-priv||app|Pod|sidecar-2',
        'Kyverno is recording',
      );
      await testCopyAt(
        'K8sRequiredLabels/team|||Namespace|demo',
        'Gatekeeper is recording',
      );
    },
  );
}
