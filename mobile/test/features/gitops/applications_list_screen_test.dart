// Widget tests for the GitOps Applications list screen.
//
// Coverage:
//   * `detected: ""` (status endpoint reports not installed) →
//     FeatureUnavailableState.gitops() renders, list does not.
//   * Happy path: two apps + summary; rows render with tool labels +
//     status pills.
//   * Filter chip: tap "Argo CD" → Flux row disappears.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/gitops/applications_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pumpList(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const ApplicationsListScreen(),
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
  // Let two async fetches (status + applications) settle.
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

Map<String, Object?> _statusInstalled() => {
      'data': {
        'detected': 'both',
        'argocd': {'available': true, 'appSetsAvailable': true},
        'fluxcd': {
          'available': true,
          'controllers': ['source', 'kustomize'],
        },
      },
    };

Map<String, Object?> _statusNotDetected() => {
      'data': {
        'detected': '',
        'argocd': {'available': false},
        'fluxcd': {'available': false},
      },
    };

Map<String, Object?> _twoApps() => {
      'data': {
        'applications': [
          {
            'id': 'argo:argocd:my-argo-app',
            'name': 'my-argo-app',
            'namespace': 'argocd',
            'tool': 'argocd',
            'kind': 'Application',
            'syncStatus': 'synced',
            'healthStatus': 'healthy',
            'source': {'repoURL': 'https://example.com/r'},
          },
          {
            'id': 'flux-ks:flux-system:my-flux-app',
            'name': 'my-flux-app',
            'namespace': 'flux-system',
            'tool': 'fluxcd',
            'kind': 'Kustomization',
            'syncStatus': 'outofsync',
            'healthStatus': 'degraded',
            'source': {'repoURL': 'https://example.com/r'},
          },
        ],
        'summary': {
          'total': 2,
          'synced': 1,
          'outOfSync': 1,
          'degraded': 1,
        },
      },
    };

void main() {
  group('ApplicationsListScreen', () {
    testWidgets('not-detected → FeatureUnavailableState.gitops()',
        (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/gitops/status', body: _statusNotDetected());
      // Even if list call goes out, mock returns canned 404; we should
      // never reach it because status gates the surface.
      mock.onJson('GET', '/api/v1/gitops/applications', body: {
        'data': {'applications': <Map<String, Object?>>[]},
      });

      await _pumpList(tester, mock);

      expect(
        find.textContaining('GitOps controller'),
        findsOneWidget,
      );
      // The summary chips shouldn't render — we're on the unavailable
      // state, not the list body.
      expect(find.textContaining('Synced'), findsNothing);
    });

    testWidgets('detected → renders rows with tool labels', (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/gitops/status', body: _statusInstalled())
        ..onJson('GET', '/api/v1/gitops/applications', body: _twoApps());

      await _pumpList(tester, mock);

      expect(find.text('my-argo-app'), findsOneWidget);
      expect(find.text('my-flux-app'), findsOneWidget);
      expect(find.text('Argo CD'), findsWidgets);
      expect(find.text('Flux'), findsWidgets);
    });

    testWidgets('tool filter chip narrows the visible rows', (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/gitops/status', body: _statusInstalled())
        ..onJson('GET', '/api/v1/gitops/applications', body: _twoApps());

      await _pumpList(tester, mock);

      // Tap the "Argo CD" filter chip. (Multiple "Argo CD" texts exist:
      // chip label + tool badge.) Use the chip explicitly.
      final argoChip = find.widgetWithText(ChoiceChip, 'Argo CD');
      expect(argoChip, findsOneWidget);
      await tester.tap(argoChip);
      await tester.pumpAndSettle();

      expect(find.text('my-argo-app'), findsOneWidget);
      expect(find.text('my-flux-app'), findsNothing);
    });
  });
}
