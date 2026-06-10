// Widget tests for the mesh dashboard.
//
// Coverage:
//   * detected=='' → FeatureUnavailableState.mesh()
//   * Istio-only → Istio card "Installed" + Linkerd card "Not installed"
//   * Routing + mTLS CTAs render when at least one mesh is installed

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/mesh/mesh_dashboard_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const MeshDashboardScreen(),
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

Map<String, Object?> _notDetected() => {
      'data': {
        'status': {
          'detected': '',
          'istio': {'installed': false},
          'linkerd': {'installed': false},
        },
      },
    };

Map<String, Object?> _istioOnly() => {
      'data': {
        'status': {
          'detected': 'istio',
          'istio': {
            'installed': true,
            'namespace': 'istio-system',
            'version': '1.22.0',
            'mode': 'sidecar',
          },
          'linkerd': {'installed': false},
          'lastChecked': '2026-05-12T10:00:00Z',
        },
      },
    };

void main() {
  testWidgets('detected=="" renders FeatureUnavailableState.mesh()',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _notDetected());

    await _pump(tester, mock);

    expect(find.textContaining('service mesh'), findsOneWidget);
    expect(find.text('Routing rules'), findsNothing);
  });

  testWidgets('Istio-only shows Installed/Not installed cards',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _istioOnly());

    await _pump(tester, mock);

    expect(find.text('Istio'), findsOneWidget);
    expect(find.text('Linkerd'), findsOneWidget);
    expect(find.text('Installed'), findsOneWidget);
    expect(find.text('Not installed'), findsOneWidget);
    // Engine details surface for admin (we mock the admin shape with
    // namespace + version + mode set).
    expect(find.text('istio-system'), findsOneWidget);
    expect(find.text('1.22.0'), findsOneWidget);
    expect(find.text('sidecar'), findsOneWidget);
  });

  testWidgets('actions row offers routing + mTLS CTAs when installed',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _istioOnly());

    await _pump(tester, mock);

    expect(find.text('Routing rules'), findsOneWidget);
    expect(find.text('mTLS posture'), findsOneWidget);
  });
}
