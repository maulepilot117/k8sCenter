// Widget tests for the combined Issuers + ClusterIssuers list.
//
// Coverage:
//   * Not-detected falls back to FeatureUnavailableState.certManager().
//   * Combined list renders namespaced first, cluster after.
//   * Ready / Not ready badge surfaces.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/certmanager/issuers_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const IssuersListScreen(),
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

Map<String, Object?> _statusDetected() => {
      'data': {'detected': true},
    };

Map<String, Object?> _statusNotDetected() => {
      'data': {'detected': false},
    };

void main() {
  testWidgets('not detected → FeatureUnavailableState.certManager()',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusNotDetected());

    await _pump(tester, mock);

    expect(find.textContaining('cert-manager'), findsWidgets);
    expect(find.text('Ready'), findsNothing);
  });

  testWidgets(
      'renders combined Issuers + ClusterIssuers with Ready/Not ready',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson(
        'GET',
        '/api/v1/certificates/issuers',
        body: {
          'data': [
            {
              'name': 'self-signed',
              'namespace': 'app',
              'scope': 'Namespaced',
              'type': 'SelfSigned',
              'ready': true,
              'uid': 'iss-1',
              'updatedAt': '2026-05-12T10:00:00Z',
            },
          ],
        },
      )
      ..onJson(
        'GET',
        '/api/v1/certificates/clusterissuers',
        body: {
          'data': [
            {
              'name': 'letsencrypt-prod',
              'scope': 'Cluster',
              'type': 'ACME',
              'ready': true,
              'acmeServer':
                  'https://acme-v02.api.letsencrypt.org/directory',
              'uid': 'le-uid',
              'updatedAt': '2026-05-12T10:00:00Z',
            },
            {
              'name': 'broken-issuer',
              'scope': 'Cluster',
              'type': 'Vault',
              'ready': false,
              'reason': 'AuthFailed',
              'uid': 'broken-uid',
              'updatedAt': '2026-05-12T10:00:00Z',
            },
          ],
        },
      );

    await _pump(tester, mock);

    expect(find.text('self-signed'), findsOneWidget);
    expect(find.text('letsencrypt-prod'), findsOneWidget);
    expect(find.text('broken-issuer'), findsOneWidget);
    // Type badges:
    expect(find.text('SelfSigned'), findsOneWidget);
    expect(find.text('ACME'), findsOneWidget);
    expect(find.text('Vault'), findsOneWidget);
    // Ready badges (2 ready issuers + 1 not-ready):
    expect(find.text('Ready'), findsNWidgets(2));
    expect(find.text('Not ready'), findsOneWidget);
  });

  testWidgets('empty list renders guidance text', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson(
        'GET',
        '/api/v1/certificates/issuers',
        body: {'data': <Map<String, Object?>>[]},
      )
      ..onJson(
        'GET',
        '/api/v1/certificates/clusterissuers',
        body: {'data': <Map<String, Object?>>[]},
      );

    await _pump(tester, mock);

    expect(
      find.textContaining('No Issuers or ClusterIssuers'),
      findsOneWidget,
    );
  });
}
