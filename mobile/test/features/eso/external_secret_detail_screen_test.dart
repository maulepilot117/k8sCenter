// Widget tests for the ExternalSecret detail screen.
//
// PR-4h-review #4: The PR-3f-derived "drift Unknown ≠ red" regression
// guard previously covered DriftPill in isolation and the list-screen
// render. It did NOT cover the DETAIL screen — which is the only surface
// where a live `driftStatus=Unknown` actually appears (the detail endpoint
// always populates it via the impersonated `get secret`). A future
// `_HeaderCard` refactor that drops the DriftPill or recolors it would
// evade all three prior guards; this test closes that hole.
//
// PR-4h-review #24: `_StoreRefLink` distinguishes namespaced
// `SecretStore` from `ClusterSecretStore` references when wiring deep
// links. Both branches are exercised here so a routing-shape regression
// (e.g. emitting a namespaced URL for a cluster-store ref) is caught.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/eso/external_secret_detail_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

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

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  // Synthetic deep-link path matches the production app_router shape so
  // any encoding/Uri assertions in the screen behave the same way.
  final router = GoRouter(
    initialLocation: '/clusters/local/eso/externalsecrets/app/edge',
    routes: [
      GoRoute(
        path: '/clusters/:clusterId/eso/externalsecrets/:namespace/:name',
        builder: (context, state) => ExternalSecretDetailScreen(
          namespace: state.pathParameters['namespace']!,
          name: state.pathParameters['name']!,
        ),
      ),
      // Sinks for the deep-link assertions in #24.
      GoRoute(
        path: '/clusters/:clusterId/eso/stores/:namespace/:name',
        builder: (context, state) => const _Sink(label: 'namespaced'),
      ),
      GoRoute(
        path: '/clusters/:clusterId/eso/cluster-stores/:name',
        builder: (context, state) => const _Sink(label: 'cluster'),
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

class _Sink extends StatelessWidget {
  const _Sink({required this.label});
  final String label;
  @override
  Widget build(BuildContext context) =>
      Scaffold(body: Center(child: Text('SINK:$label')));
}

Map<String, Object?> _statusDetected() => {
      'data': {
        'detected': true,
        'namespace': 'external-secrets',
        'version': '0.14.0',
        'lastChecked': '2026-05-12T10:00:00Z',
      },
    };

Map<String, Object?> _detailBody({
  required String driftStatus,
  String? driftUnknownReason,
  Map<String, String>? storeRef,
}) =>
    {
      'data': {
        'name': 'edge',
        'namespace': 'app',
        'uid': 'es-1',
        'status': 'Synced',
        'storeRef': storeRef ?? {'name': 'vault', 'kind': 'SecretStore'},
        'driftStatus': driftStatus,
        // The null-aware marker `?:` applies to nullable KEYS in map
        // literals; here the key is a literal String and only the value
        // is nullable, so the lint suggestion does not apply.
        // ignore: use_null_aware_elements
        if (driftUnknownReason != null)
          'driftUnknownReason': driftUnknownReason,
      },
    };

void main() {
  testWidgets(
    'drift Unknown on the detail screen renders textMuted, never error '
    '(#4 — detail-screen regression guard)',
    (tester) async {
      final mock = MockDioAdapter();
      mock.onJson('GET', '/api/v1/externalsecrets/status',
          body: _statusDetected());
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/externalsecrets/app/edge',
        body: _detailBody(
          driftStatus: 'Unknown',
          driftUnknownReason: 'noSyncedRv',
        ),
      );

      await _pump(tester, mock);

      // The DriftPill renders "Unknown" — pin its colour.
      final unknownTexts = find.text('Unknown');
      expect(unknownTexts, findsAtLeastNWidgets(1));
      final text = tester.widget<Text>(unknownTexts.first);
      final ctx = tester.element(unknownTexts.first);
      final colors = Theme.of(ctx).extension<KubeColors>()!;
      expect(text.style?.color, colors.textMuted,
          reason: 'Drift Unknown on the detail screen MUST be textMuted, '
              'NEVER error. This is where live driftStatus=Unknown actually '
              'surfaces (the list screen only sees the poller hint).');
      expect(text.style?.color, isNot(colors.error));
      expect(text.style?.color, isNot(colors.warning));
    },
  );

  testWidgets(
    '_StoreRefLink routes a SecretStore ref to the namespaced store URL '
    '(#24 — namespaced branch)',
    (tester) async {
      final mock = MockDioAdapter();
      mock.onJson('GET', '/api/v1/externalsecrets/status',
          body: _statusDetected());
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/externalsecrets/app/edge',
        body: _detailBody(
          driftStatus: 'InSync',
          storeRef: const {'name': 'vault', 'kind': 'SecretStore'},
        ),
      );

      await _pump(tester, mock);

      // Tap on the store ref link. Pattern: find the InkWell wrapping
      // "SecretStore / vault" by tapping on the link text and confirming
      // the namespaced sink rendered.
      await tester.tap(find.text('SecretStore / vault'));
      await tester.pumpAndSettle();
      expect(find.text('SINK:namespaced'), findsOneWidget,
          reason: 'A SecretStore ref must deep-link to the namespaced '
              'store URL, not the cluster-scope path.');
    },
  );

  testWidgets(
    '_StoreRefLink routes a ClusterSecretStore ref to the cluster URL '
    '(#24 — cluster-scoped branch)',
    (tester) async {
      final mock = MockDioAdapter();
      mock.onJson('GET', '/api/v1/externalsecrets/status',
          body: _statusDetected());
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/externalsecrets/app/edge',
        body: _detailBody(
          driftStatus: 'InSync',
          storeRef: const {'name': 'global', 'kind': 'ClusterSecretStore'},
        ),
      );

      await _pump(tester, mock);

      await tester.tap(find.text('ClusterSecretStore / global'));
      await tester.pumpAndSettle();
      expect(find.text('SINK:cluster'), findsOneWidget,
          reason: 'A ClusterSecretStore ref must deep-link to the '
              'cluster-scope store URL, never the namespaced one.');
    },
  );
}
