// Widget tests for the route detail screen.
//
// Coverage:
//   * Malformed composite id renders an inline validation error.
//   * Happy path renders header + metadata + matchers + destinations.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/mesh/route_detail_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(
  WidgetTester tester,
  MockDioAdapter mock,
  String id,
) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
    child: _DioInstaller(
      mock: mock,
      child: MaterialApp(
        theme: buildKubeTheme('liquid-glass'),
        home: MeshRouteDetailScreen(id: id),
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
  testWidgets('malformed composite id renders validation error',
      (tester) async {
    final mock = MockDioAdapter();
    await _pump(tester, mock, 'bogus-not-colons');
    expect(find.textContaining('Invalid route ID'), findsOneWidget);
  });

  testWidgets('raw=null hides the RAW SPEC panel', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/routing/istio%3Aapp%3Avs%3Anull-raw',
          body: {
            'data': {
              'id': 'istio:app:vs:null-raw',
              'mesh': 'istio',
              'kind': 'VirtualService',
              'name': 'null-raw',
              'namespace': 'app',
            },
          });

    await _pump(tester, mock, 'istio:app:vs:null-raw');

    // The _RawPanel is only rendered when route.raw != null.
    expect(find.text('RAW SPEC'), findsNothing);
  });

  testWidgets('happy path renders metadata + matchers', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/routing/istio%3Aapp%3Avs%3Aweb', body: {
        'data': {
          'id': 'istio:app:vs:web',
          'mesh': 'istio',
          'kind': 'VirtualService',
          'name': 'web',
          'namespace': 'app',
          'hosts': ['web.example.com'],
          'destinations': [
            {'host': 'web.app.svc.cluster.local', 'port': 80, 'weight': 100},
          ],
          'matchers': [
            {'method': 'GET', 'pathPrefix': '/api', 'name': 'api-prefix'},
          ],
          'raw': {
            'apiVersion': 'networking.istio.io/v1',
            'kind': 'VirtualService',
            'metadata': {'name': 'web', 'namespace': 'app'},
          },
        },
      });

    await _pump(tester, mock, 'istio:app:vs:web');

    expect(find.text('VirtualService'), findsAtLeastNWidgets(1));
    expect(find.textContaining('web.example.com'), findsAtLeastNWidgets(1));
    expect(find.text('GET'), findsOneWidget);
    expect(find.textContaining('/api'), findsAtLeastNWidgets(1));
    // Matcher name suffix.
    expect(find.text('api-prefix'), findsOneWidget);
    // Destination row has the FQDN.
    expect(find.textContaining('web.app.svc.cluster.local'), findsOneWidget);
  });
}
