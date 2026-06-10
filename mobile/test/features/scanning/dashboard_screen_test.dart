// Widget tests for the Vulnerability dashboard.
//
// Coverage:
//   * `detected: false` falls back to `FeatureUnavailableState.scanning`.
//   * Both scanners installed → header card + two scanner cards render.
//   * Only Trivy installed → Kubescape card shows "Not installed".
//   * 5xx → retry-able "temporarily unavailable" panel.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/scanning/dashboard_screen.dart';
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
        builder: (context, state) => const ScanningDashboardScreen(),
      ),
      // Stub for the browse tile tap so the tap doesn't 404 the
      // test router.
      GoRoute(
        path: '/clusters/:clusterId/scanning/vulnerabilities',
        builder: (context, state) => const Scaffold(body: Text('list')),
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

void main() {
  testWidgets('detected:false → FeatureUnavailableState.scanning',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: {
        'data': {'detected': ''},
      });

    await _pump(tester, mock);
    expect(find.textContaining('vulnerability scanner'), findsOneWidget);
    expect(find.textContaining('not installed on this cluster'),
        findsOneWidget);
  });

  testWidgets('both scanners → header + cards render', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: {
        'data': {
          'detected': 'both',
          'trivy': {'available': true, 'namespace': 'trivy-system'},
          'kubescape': {'available': true, 'namespace': 'kubescape'},
          'lastChecked': '2026-05-15T12:00:00Z',
        },
      });

    await _pump(tester, mock);
    expect(find.text('Trivy + Kubescape detected'), findsOneWidget);
    expect(find.text('Installed'), findsNWidgets(2));
    expect(find.text('Trivy'), findsWidgets);
    expect(find.text('Kubescape'), findsWidgets);
    expect(find.text('Browse workload vulnerabilities'), findsOneWidget);
  });

  testWidgets('only Trivy installed → Kubescape card shows Not installed',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: {
        'data': {
          'detected': 'trivy',
          'trivy': {'available': true, 'namespace': 'trivy-system'},
          'kubescape': {'available': false},
        },
      });

    await _pump(tester, mock);
    expect(find.text('Trivy detected'), findsOneWidget);
    expect(find.text('Installed'), findsOneWidget);
    expect(find.text('Not installed'), findsOneWidget,
        reason: 'Kubescape card must visibly indicate it is not installed.');
  });

  testWidgets('5xx renders retry-able error panel', (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/scanning/status',
        status: 503,
        body: {
          'error': {'code': 503, 'message': 'discovery offline'},
        },
      );

    await _pump(tester, mock);
    expect(find.textContaining('temporarily unavailable'), findsOneWidget,
        reason:
            'Repository collapses 5xx to `unreachable` so the screen '
            'surfaces a retry-able panel rather than the install '
            'install-guidance copy operators would read as terminal.');
    expect(find.text('Retry'), findsOneWidget,
        reason: 'Transient 5xx must remain retry-able.');
  });
}
