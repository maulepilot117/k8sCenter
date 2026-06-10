// Widget tests for the application detail screen's tab gating.
//
// Coverage:
//   * HelmRelease (id `flux-hr:*`) hides Resources + History tabs.
//   * Argo Application (id `argo:*`) shows all four tabs.
//   * Flux Kustomization (id `flux-ks:*`) shows all four tabs.
//   * Invalid composite id renders the "open from the list" inline
//     error instead of crashing.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/gitops/application_detail_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

ResponseBody _errorJson(Object body, {required int status}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

Future<void> _pumpDetail(
  WidgetTester tester, {
  required String id,
}) async {
  final mock = MockDioAdapter();
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => ApplicationDetailScreen(id: id),
      ),
    ],
  );
  await tester.pumpWidget(ProviderScope(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
    child: _MockedDio(
      mock: mock,
      child: MaterialApp.router(
        theme: buildKubeTheme('liquid-glass'),
        routerConfig: router,
      ),
    ),
  ));
  // First pump initializes providers + Dio; let inflight requests
  // settle by pumping the event queue + a couple of frames.
  await tester.pump();
}

/// Wraps the child after installing the mock adapter into the
/// ProviderScope's dio. The adapter lives outside the scope so each
/// test can pre-register responses before pumping.
class _MockedDio extends ConsumerWidget {
  const _MockedDio({required this.mock, required this.child});

  final MockDioAdapter mock;
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    ref.read(dioProvider).httpClientAdapter = mock;
    return child;
  }
}

Map<String, Object?> _argoAppEnvelope(String id) => {
      'data': {
        'app': {
          'id': id,
          'name': id.split(':').last,
          'namespace': 'argocd',
          'tool': 'argocd',
          'kind': 'Application',
          'syncStatus': 'synced',
          'healthStatus': 'healthy',
          'source': {'repoURL': 'https://example.com/r'},
        },
        'resources': [
          {
            'kind': 'Deployment',
            'namespace': 'default',
            'name': 'web',
            'status': 'Synced',
          },
        ],
        'history': [
          {
            'revision': '0123456789abcdef',
            'status': 'Synced',
            'deployedAt': '2026-05-11T12:00:00Z',
          },
        ],
      },
    };

Map<String, Object?> _fluxHrEnvelope(String id) => {
      'data': {
        'app': {
          'id': id,
          'name': id.split(':').last,
          'namespace': 'flux-system',
          'tool': 'fluxcd',
          'kind': 'HelmRelease',
          'syncStatus': 'synced',
          'healthStatus': 'healthy',
          'source': {'chartName': 'redis'},
        },
        // Backend omits resources + history for HelmRelease detail.
      },
    };

Map<String, Object?> _fluxKsEnvelope(String id) => {
      'data': {
        'app': {
          'id': id,
          'name': id.split(':').last,
          'namespace': 'flux-system',
          'tool': 'fluxcd',
          'kind': 'Kustomization',
          'syncStatus': 'synced',
          'healthStatus': 'healthy',
          'source': {'repoURL': 'https://example.com/r', 'path': 'apps/web'},
        },
        'resources': [
          {
            'kind': 'ConfigMap',
            'namespace': 'default',
            'name': 'cm',
            'status': 'Synced',
          },
        ],
        'history': <Map<String, Object?>>[],
      },
    };

void main() {
  group('ApplicationDetailScreen tab gating', () {
    testWidgets('Argo Application renders all four tabs', (tester) async {
      final mock = MockDioAdapter();
      mock.onJson(
        'GET',
        '/api/v1/gitops/applications/argo%3Aargocd%3Amy-app',
        body: _argoAppEnvelope('argo:argocd:my-app'),
      );
      final router = GoRouter(
        initialLocation: '/',
        routes: [
          GoRoute(
            path: '/',
            builder: (context, state) => const ApplicationDetailScreen(
              id: 'argo:argocd:my-app',
            ),
          ),
        ],
      );
      await tester.pumpWidget(ProviderScope(
        overrides: [
          backendUrlProvider.overrideWithValue('http://test'),
          secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        ],
        child: _MockedDio(
          mock: mock,
          child: MaterialApp.router(
            theme: buildKubeTheme('liquid-glass'),
            routerConfig: router,
          ),
        ),
      ));
      // Let the Dio chain settle.
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));
      await tester.pumpAndSettle(const Duration(milliseconds: 200));

      expect(find.text('Overview'), findsOneWidget);
      expect(find.text('Resources'), findsOneWidget);
      expect(find.text('History'), findsOneWidget);
      expect(find.text('Events'), findsOneWidget);
    });

    testWidgets('Flux HelmRelease hides Resources + History', (tester) async {
      final mock = MockDioAdapter();
      mock.onJson(
        'GET',
        '/api/v1/gitops/applications/flux-hr%3Aflux-system%3Amy-release',
        body: _fluxHrEnvelope('flux-hr:flux-system:my-release'),
      );
      final router = GoRouter(
        initialLocation: '/',
        routes: [
          GoRoute(
            path: '/',
            builder: (context, state) => const ApplicationDetailScreen(
              id: 'flux-hr:flux-system:my-release',
            ),
          ),
        ],
      );
      await tester.pumpWidget(ProviderScope(
        overrides: [
          backendUrlProvider.overrideWithValue('http://test'),
          secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        ],
        child: _MockedDio(
          mock: mock,
          child: MaterialApp.router(
            theme: buildKubeTheme('liquid-glass'),
            routerConfig: router,
          ),
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));
      await tester.pumpAndSettle(const Duration(milliseconds: 200));

      expect(find.text('Overview'), findsOneWidget);
      expect(find.text('Resources'), findsNothing);
      expect(find.text('History'), findsNothing);
      expect(find.text('Events'), findsOneWidget);
    });

    testWidgets('Flux Kustomization renders all four tabs', (tester) async {
      final mock = MockDioAdapter();
      mock.onJson(
        'GET',
        '/api/v1/gitops/applications/flux-ks%3Aflux-system%3Amy-ks',
        body: _fluxKsEnvelope('flux-ks:flux-system:my-ks'),
      );
      final router = GoRouter(
        initialLocation: '/',
        routes: [
          GoRoute(
            path: '/',
            builder: (context, state) => const ApplicationDetailScreen(
              id: 'flux-ks:flux-system:my-ks',
            ),
          ),
        ],
      );
      await tester.pumpWidget(ProviderScope(
        overrides: [
          backendUrlProvider.overrideWithValue('http://test'),
          secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        ],
        child: _MockedDio(
          mock: mock,
          child: MaterialApp.router(
            theme: buildKubeTheme('liquid-glass'),
            routerConfig: router,
          ),
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));
      await tester.pumpAndSettle(const Duration(milliseconds: 200));

      expect(find.text('Overview'), findsOneWidget);
      expect(find.text('Resources'), findsOneWidget);
      expect(find.text('History'), findsOneWidget);
      expect(find.text('Events'), findsOneWidget);
    });

    testWidgets('Invalid composite id renders inline error', (tester) async {
      await _pumpDetail(tester, id: 'not-a-composite-id');
      expect(
        find.textContaining('Invalid application ID'),
        findsOneWidget,
      );
    });

    testWidgets('404 response renders humanised not-found message',
        (tester) async {
      final mock = MockDioAdapter();
      mock.on(
        'GET',
        '/api/v1/gitops/applications/argo%3Aargocd%3Amy-app',
        (_) => _errorJson({
          'error': {'code': 404, 'message': 'not found'},
        }, status: 404),
      );
      final router = GoRouter(
        initialLocation: '/',
        routes: [
          GoRoute(
            path: '/',
            builder: (context, state) => const ApplicationDetailScreen(
              id: 'argo:argocd:my-app',
            ),
          ),
        ],
      );
      await tester.pumpWidget(ProviderScope(
        overrides: [
          backendUrlProvider.overrideWithValue('http://test'),
          secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        ],
        child: _MockedDio(
          mock: mock,
          child: MaterialApp.router(
            theme: buildKubeTheme('liquid-glass'),
            routerConfig: router,
          ),
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));
      await tester.pumpAndSettle(const Duration(milliseconds: 200));

      expect(find.textContaining('was not found'), findsOneWidget);
    });

    testWidgets('403 response renders humanised access-denied message',
        (tester) async {
      final mock = MockDioAdapter();
      mock.on(
        'GET',
        '/api/v1/gitops/applications/argo%3Aargocd%3Amy-app',
        (_) => _errorJson({
          'error': {'code': 403, 'message': 'forbidden'},
        }, status: 403),
      );
      final router = GoRouter(
        initialLocation: '/',
        routes: [
          GoRoute(
            path: '/',
            builder: (context, state) => const ApplicationDetailScreen(
              id: 'argo:argocd:my-app',
            ),
          ),
        ],
      );
      await tester.pumpWidget(ProviderScope(
        overrides: [
          backendUrlProvider.overrideWithValue('http://test'),
          secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        ],
        child: _MockedDio(
          mock: mock,
          child: MaterialApp.router(
            theme: buildKubeTheme('liquid-glass'),
            routerConfig: router,
          ),
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));
      await tester.pumpAndSettle(const Duration(milliseconds: 200));

      expect(find.textContaining('permission'), findsOneWidget);
    });
  });
}
