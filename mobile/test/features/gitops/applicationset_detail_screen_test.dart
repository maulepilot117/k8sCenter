// Widget tests for the ApplicationSet detail screen.
//
// Coverage:
//   (a) invalid composite ID renders error state
//   (b) 0 generated apps renders the empty-card label
//   (c) 404 response surfaces a humanised "not found" error
//   (d) 403 response surfaces a humanised "permission" error
//   (e) conditions panel toggles on tap

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/gitops/applicationset_detail_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

ResponseBody _json(Object body, {int status = 200}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
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

Future<void> _pump(
  WidgetTester tester,
  MockDioAdapter mock, {
  String id = 'argo-as:argocd:my-set',
}) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => ApplicationSetDetailScreen(id: id),
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
  await tester.pump(const Duration(milliseconds: 50));
  await tester.pumpAndSettle(const Duration(milliseconds: 200));
}

Map<String, Object?> _appSetEnvelope({
  List<Map<String, Object?>> applications = const [],
  List<Map<String, Object?>> conditions = const [],
}) {
  return {
    'data': {
      'appSet': {
        'id': 'argo-as:argocd:my-set',
        'name': 'my-set',
        'namespace': 'argocd',
        'tool': 'argocd',
        'generatorTypes': ['list'],
        'templateSource': {'repoURL': 'https://example.com/r'},
        'templateDestination': 'in-cluster/default',
        'status': 'Healthy',
        'generatedAppCount': applications.length,
      },
      'generators': [
        {
          'list': {
            'elements': [
              {'cluster': 'staging'},
            ],
          },
        },
      ],
      'conditions': conditions,
      'applications': applications,
    },
  };
}

void main() {
  group('ApplicationSetDetailScreen', () {
    testWidgets('(a) invalid composite ID renders error state', (tester) async {
      final mock = MockDioAdapter();
      await _pump(tester, mock, id: 'not-a-valid-id');

      expect(find.textContaining('Invalid ApplicationSet ID'), findsOneWidget);
    });

    testWidgets('(b) 0 generated apps renders empty-card label',
        (tester) async {
      final mock = MockDioAdapter()
        ..on(
          'GET',
          '/api/v1/gitops/applicationsets/argo-as%3Aargocd%3Amy-set',
          (_) => _json(_appSetEnvelope()),
        );

      await _pump(tester, mock);

      expect(find.textContaining('No generated applications'), findsOneWidget);
    });

    testWidgets('(c) 404 response renders humanised not-found message',
        (tester) async {
      final mock = MockDioAdapter()
        ..on(
          'GET',
          '/api/v1/gitops/applicationsets/argo-as%3Aargocd%3Amy-set',
          (_) => _json({
            'error': {'code': 404, 'message': 'not found'},
          }, status: 404),
        );

      await _pump(tester, mock);

      expect(find.textContaining('was not found'), findsOneWidget);
    });

    testWidgets('(d) 403 response renders humanised access-denied message',
        (tester) async {
      final mock = MockDioAdapter()
        ..on(
          'GET',
          '/api/v1/gitops/applicationsets/argo-as%3Aargocd%3Amy-set',
          (_) => _json({
            'error': {'code': 403, 'message': 'forbidden'},
          }, status: 403),
        );

      await _pump(tester, mock);

      expect(find.textContaining('permission'), findsOneWidget);
    });

    testWidgets('(e) conditions panel toggles on tap', (tester) async {
      final mock = MockDioAdapter()
        ..on(
          'GET',
          '/api/v1/gitops/applicationsets/argo-as%3Aargocd%3Amy-set',
          (_) => _json(_appSetEnvelope(
            conditions: [
              {
                'type': 'ErrorOccurred',
                'status': 'True',
                'reason': 'GitFetchError',
                'message': 'failed to clone repo',
              },
            ],
          )),
        );

      await _pump(tester, mock);

      // Conditions panel header is visible; message is collapsed.
      expect(find.textContaining('Conditions'), findsOneWidget);
      expect(find.text('failed to clone repo'), findsNothing);

      // Tap to expand.
      await tester.tap(find.textContaining('Conditions'));
      await tester.pumpAndSettle();

      expect(find.text('failed to clone repo'), findsOneWidget);

      // Tap again to collapse.
      await tester.tap(find.textContaining('Conditions'));
      await tester.pumpAndSettle();

      expect(find.text('failed to clone repo'), findsNothing);
    });
  });
}
