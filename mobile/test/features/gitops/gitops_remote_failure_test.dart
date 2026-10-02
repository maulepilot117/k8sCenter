// R-8 remote-failure rendering on the GitOps list screens: a status payload
// whose `reason` says the backend could not tell must not read as "not
// installed", and a status/list error carrying an R-8 reason must render
// the typed state rather than the raw error text.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/gitops/applications_list_screen.dart';
import 'package:kubecenter/features/gitops/applicationsets_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(
  WidgetTester tester,
  MockDioAdapter mock,
  Widget screen,
) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [GoRoute(path: '/', builder: (context, state) => screen)],
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

Map<String, Object?> _status({String? reason}) => {
      'data': {
        'detected': '',
        'argocd': {'available': false},
        'fluxcd': {'available': false},
        'reason': ?reason,
      },
    };

Map<String, Object?> _envelope(int code, String reason) => {
      'error': {'code': code, 'message': 'raw backend text', 'reason': reason},
    };

void main() {
  const screens = <String, Widget>{
    'ApplicationsListScreen': ApplicationsListScreen(),
    'ApplicationSetsListScreen': ApplicationSetsListScreen(),
  };

  for (final entry in screens.entries) {
    group(entry.key, () {
      testWidgets('status reason unreachable renders the typed state with '
          'Retry, not the not-installed card', (tester) async {
        final mock = MockDioAdapter()
          ..onJson('GET', '/api/v1/gitops/status',
              body: _status(reason: 'unreachable'));

        await _pump(tester, mock, entry.value);

        expect(find.text('Cluster unreachable'), findsOneWidget);
        expect(find.text('Retry'), findsOneWidget);
        expect(find.textContaining('is not installed on this cluster'),
            findsNothing);
      });

      testWidgets('status without reason renders the not-installed card '
          'with no Retry', (tester) async {
        final mock = MockDioAdapter()
          ..onJson('GET', '/api/v1/gitops/status', body: _status());

        await _pump(tester, mock, entry.value);

        expect(find.textContaining('is not installed on this cluster'),
            findsOneWidget);
        expect(find.text('Retry'), findsNothing);
      });

      testWidgets('status reason discovery_missing is a real not-installed',
          (tester) async {
        final mock = MockDioAdapter()
          ..onJson('GET', '/api/v1/gitops/status',
              body: _status(reason: 'discovery_missing'));

        await _pump(tester, mock, entry.value);

        expect(find.textContaining('is not installed on this cluster'),
            findsOneWidget);
        expect(find.text('Retry'), findsNothing);
      });

      testWidgets('502 unreachable on the status route is not swallowed '
          'into not-installed', (tester) async {
        final mock = MockDioAdapter()
          ..onJson('GET', '/api/v1/gitops/status',
              status: 502, body: _envelope(502, 'unreachable'));

        await _pump(tester, mock, entry.value);

        expect(find.text('Cluster unreachable'), findsOneWidget);
        expect(find.text('Retry'), findsOneWidget);
        expect(find.textContaining('is not installed on this cluster'),
            findsNothing);
      });

      testWidgets('503 db_unavailable on the status route renders the typed '
          'state, not not-installed', (tester) async {
        final mock = MockDioAdapter()
          ..onJson('GET', '/api/v1/gitops/status',
              status: 503, body: _envelope(503, 'db_unavailable'));

        await _pump(tester, mock, entry.value);

        expect(find.textContaining('is not installed on this cluster'),
            findsNothing);
        expect(find.text('Retry'), findsOneWidget);
      });
    });
  }

  testWidgets('ApplicationsListScreen: list 502 credentials_invalid renders '
      'typed state with no Retry', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/gitops/status', body: {
        'data': {'detected': 'argocd'},
      })
      ..onJson('GET', '/api/v1/gitops/applications',
          status: 502, body: _envelope(502, 'credentials_invalid'));

    await _pump(tester, mock, const ApplicationsListScreen());

    expect(find.text('Credentials no longer valid'), findsOneWidget);
    expect(find.text('Failed to load applications'), findsNothing);
    expect(find.text('Retry'), findsNothing);
    expect(find.textContaining('raw backend text'), findsNothing);
  });
}
