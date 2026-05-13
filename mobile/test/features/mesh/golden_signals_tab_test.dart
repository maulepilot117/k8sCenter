// Widget tests for the golden signals tab.
//
// Coverage:
//   * `available: false` renders the unavailable banner.
//   * Happy path renders tile values (RPS / error rate / p50/p95/p99).
//   * `missingQueries` non-empty renders the banner + dashes for the
//     affected tiles.
//   * Both-meshes-installed without selection renders the mesh prompt.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/mesh_repository.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/mesh/golden_signals_tab.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(
  WidgetTester tester,
  MockDioAdapter mock, {
  required MeshStatus status,
}) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
    child: _DioInstaller(
      mock: mock,
      child: MaterialApp(
        theme: buildKubeTheme('nexus'),
        home: Scaffold(
          body: GoldenSignalsTab(
            namespace: 'app',
            service: 'web',
            status: status,
          ),
        ),
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

const _istioStatus = MeshStatus(
  detected: 'istio',
  istio: MeshInfo(installed: true),
  linkerd: MeshInfo(installed: false),
);

const _bothStatus = MeshStatus(
  detected: 'both',
  istio: MeshInfo(installed: true),
  linkerd: MeshInfo(installed: true),
);

void main() {
  testWidgets('available=false renders Metrics unavailable banner',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/golden-signals', body: {
        'data': {
          'status': {'detected': 'istio'},
          'signals': {
            'mesh': 'istio',
            'namespace': 'app',
            'service': 'web',
            'available': false,
            'reason': 'Prometheus offline',
          },
        },
      });

    await _pump(tester, mock, status: _istioStatus);

    expect(find.text('Metrics unavailable'), findsOneWidget);
    expect(find.text('Prometheus offline'), findsOneWidget);
  });

  testWidgets('happy path renders five tiles + values', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/golden-signals', body: {
        'data': {
          'status': {'detected': 'istio'},
          'signals': {
            'mesh': 'istio',
            'namespace': 'app',
            'service': 'web',
            'available': true,
            'rps': 42.5,
            'errorRate': 0.013,
            'p50Ms': 12.0,
            'p95Ms': 75.0,
            'p99Ms': 220.0,
          },
        },
      });

    await _pump(tester, mock, status: _istioStatus);

    expect(find.text('Requests / s'), findsOneWidget);
    expect(find.text('Error rate'), findsOneWidget);
    expect(find.text('p50 latency'), findsOneWidget);
    expect(find.text('p95 latency'), findsOneWidget);
    expect(find.text('p99 latency'), findsOneWidget);
    expect(find.text('42.50'), findsOneWidget);
    expect(find.text('12.0 ms'), findsOneWidget);
    expect(find.text('220.0 ms'), findsOneWidget);
  });

  testWidgets('missingQueries renders banner + em-dash for failed tiles',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/golden-signals', body: {
        'data': {
          'status': {'detected': 'istio'},
          'signals': {
            'mesh': 'istio',
            'namespace': 'app',
            'service': 'web',
            'available': true,
            'missingQueries': ['p99', 'rps'],
            'rps': 0.0,
            'errorRate': 0.01,
            'p50Ms': 8.0,
            'p95Ms': 20.0,
            'p99Ms': 0.0,
          },
        },
      });

    await _pump(tester, mock, status: _istioStatus);

    expect(find.textContaining('2 metric(s) unavailable'), findsOneWidget);
    // Two em-dashes — the rps and p99 tiles.
    expect(find.text('—'), findsAtLeastNWidgets(2));
    // p50 still renders.
    expect(find.text('8.0 ms'), findsOneWidget);
  });

  testWidgets('both meshes installed shows mesh prompt until picked',
      (tester) async {
    // No HTTP mocking needed — the body should not fetch until a mesh
    // is selected. The default 404 from MockDioAdapter would error if
    // the request fired, so this also catches "did we fetch too early?".
    final mock = MockDioAdapter();

    await _pump(tester, mock, status: _bothStatus);

    expect(find.textContaining('Both meshes are installed'), findsOneWidget);
    expect(find.text('Requests / s'), findsNothing);
  });
}
