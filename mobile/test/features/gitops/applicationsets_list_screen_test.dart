// Widget tests for the ApplicationSets list screen.
//
// Coverage:
//   * `appSetsAvailable: false` → custom "AppSet CRD not installed"
//     state renders (separate from the generic GitOps-not-detected
//     state).
//   * Happy path: one AppSet → row renders with name + status pill.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/gitops/applicationsets_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const ApplicationSetsListScreen(),
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
  await tester.pump(const Duration(milliseconds: 50));
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
  testWidgets(
      'appSetsAvailable: false → AppSet-CRD-unavailable state renders',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/gitops/status',
        body: {
          'data': {
            'detected': 'argocd',
            'argocd': {'available': true, 'appSetsAvailable': false},
            'fluxcd': {'available': false},
          },
        },
      );

    await _pump(tester, mock);

    expect(
      find.textContaining('ApplicationSets'),
      findsWidgets, // the AppBar title + the feature card title both match
    );
    expect(
      find.textContaining('ApplicationSet CRD is not installed'),
      findsOneWidget,
    );
  });

  testWidgets('appSetsAvailable: true → row renders with name + status',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/gitops/status',
        body: {
          'data': {
            'detected': 'argocd',
            'argocd': {'available': true, 'appSetsAvailable': true},
            'fluxcd': {'available': false},
          },
        },
      )
      ..onJson(
        'GET',
        '/api/v1/gitops/applicationsets',
        body: {
          'data': {
            'applicationSets': [
              {
                'id': 'argo-as:argocd:my-set',
                'name': 'my-set',
                'namespace': 'argocd',
                'tool': 'argocd',
                'generatorTypes': ['list'],
                'templateSource': {'repoURL': 'https://example.com/r'},
                'templateDestination': 'in-cluster/default',
                'status': 'Healthy',
                'generatedAppCount': 3,
              },
            ],
          },
        },
      );

    await _pump(tester, mock);

    expect(find.text('my-set'), findsOneWidget);
    expect(find.textContaining('3 apps'), findsOneWidget);
    expect(find.textContaining('Healthy'), findsOneWidget);
  });
}
