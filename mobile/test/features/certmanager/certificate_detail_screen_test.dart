// Widget tests for the cert-manager certificate detail screen.
//
// Coverage:
//   * Overview renders threshold attribution + DNS names.
//   * thresholdConflict=true surfaces the "Conflict — using defaults" badge.
//   * RBAC hint surfaces on Issuing + empty CR list.
//   * Sub-Resources renders all three sections.
//   * Renew/Reissue buttons render and (Renew) opens the confirm sheet.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/certmanager/certificate_detail_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const CertificateDetailScreen(
          namespace: 'app',
          name: 'web-tls',
        ),
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

Map<String, Object?> _readyDetail({
  bool thresholdConflict = false,
  bool withSubResources = true,
  String status = 'Ready',
}) =>
    {
      'data': {
        'certificate': {
          'name': 'web-tls',
          'namespace': 'app',
          'status': status,
          'issuerRef': {
            'name': 'letsencrypt-prod',
            'kind': 'ClusterIssuer',
            'group': 'cert-manager.io',
          },
          'secretName': 'web-tls-secret',
          'dnsNames': ['web.example.com', 'www.example.com'],
          'commonName': 'web.example.com',
          'notBefore': '2026-03-01T00:00:00Z',
          'notAfter': '2026-08-01T00:00:00Z',
          'renewalTime': '2026-07-01T00:00:00Z',
          'daysRemaining': 80,
          'warningThresholdDays': 30,
          'criticalThresholdDays': 7,
          'warningThresholdSource': 'issuer',
          'criticalThresholdSource': 'default',
          'thresholdSource': 'issuer',
          'thresholdConflict': thresholdConflict,
          'uid': 'cert-uid',
        },
        'certificateRequests': withSubResources
            ? [
                {
                  'name': 'web-tls-1',
                  'namespace': 'app',
                  'status': 'Ready',
                  'issuerRef': {
                    'name': 'letsencrypt-prod',
                    'kind': 'ClusterIssuer',
                  },
                  'createdAt': '2026-03-01T00:00:00Z',
                  'uid': 'cr-uid',
                },
              ]
            : <Map<String, Object?>>[],
        'orders': withSubResources
            ? [
                {
                  'name': 'web-tls-1-order',
                  'namespace': 'app',
                  'state': 'valid',
                  'createdAt': '2026-03-01T00:01:00Z',
                  'uid': 'order-uid',
                },
              ]
            : <Map<String, Object?>>[],
        'challenges': withSubResources
            ? [
                {
                  'name': 'web-tls-1-challenge',
                  'namespace': 'app',
                  'type': 'HTTP-01',
                  'state': 'valid',
                  'dnsName': 'web.example.com',
                  'createdAt': '2026-03-01T00:02:00Z',
                  'uid': 'chall-uid',
                },
              ]
            : <Map<String, Object?>>[],
      },
    };

Map<String, Object?> _emptyEvents() => {
      'data': {'items': <Map<String, Object?>>[]},
    };

void _stubCommon(MockDioAdapter mock) {
  mock.onJson(
    'GET',
    '/api/v1/resources/events',
    body: _emptyEvents(),
  );
}

void main() {
  testWidgets('Overview renders threshold attribution + DNS names',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/certificates/certificates/app/web-tls',
        body: _readyDetail(),
      );
    _stubCommon(mock);

    await _pump(tester, mock);

    expect(find.text('web-tls'), findsWidgets);
    expect(find.textContaining('web.example.com'), findsWidgets);
    // Per-key attribution renders in RichText spans, so the finder
    // must include `findRichText: true` to walk the InlineSpan tree.
    expect(
      find.textContaining('From Issuer letsencrypt-prod', findRichText: true),
      findsOneWidget,
    );
    expect(find.textContaining('Default', findRichText: true), findsWidgets);
  });

  testWidgets('thresholdConflict surfaces the "Conflict" badge',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/certificates/certificates/app/web-tls',
        body: _readyDetail(thresholdConflict: true),
      );
    _stubCommon(mock);

    await _pump(tester, mock);

    expect(find.textContaining('Conflict'), findsOneWidget);
  });

  testWidgets('Sub-Resources tab renders all three sections',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/certificates/certificates/app/web-tls',
        body: _readyDetail(),
      );
    _stubCommon(mock);

    await _pump(tester, mock);

    // Switch to Sub-Resources tab.
    await tester.tap(find.text('Sub-Resources'));
    await tester.pumpAndSettle();

    expect(find.textContaining('CERTIFICATE REQUESTS'), findsOneWidget);
    expect(find.textContaining('ORDERS'), findsOneWidget);
    expect(find.textContaining('CHALLENGES'), findsOneWidget);
    expect(find.text('web-tls-1'), findsOneWidget);
    expect(find.text('web-tls-1-order'), findsOneWidget);
    expect(find.text('web-tls-1-challenge'), findsOneWidget);
  });

  testWidgets(
      'Issuing status + empty CR list surfaces the RBAC hint banner',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/certificates/certificates/app/web-tls',
        body: _readyDetail(status: 'Issuing', withSubResources: false),
      );
    _stubCommon(mock);

    await _pump(tester, mock);

    // Switch to Sub-Resources tab.
    await tester.tap(find.text('Sub-Resources'));
    await tester.pumpAndSettle();

    expect(
      find.textContaining('Some sub-resources may be hidden by RBAC'),
      findsOneWidget,
    );
  });

  testWidgets('Renew + Re-issue buttons render in the app bar',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/certificates/certificates/app/web-tls',
        body: _readyDetail(),
      );
    _stubCommon(mock);

    await _pump(tester, mock);

    expect(find.widgetWithText(TextButton, 'Renew'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Re-issue'), findsOneWidget);
  });

  testWidgets('Renew opens the confirm sheet with the Renew CTA',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson(
        'GET',
        '/api/v1/certificates/certificates/app/web-tls',
        body: _readyDetail(),
      );
    _stubCommon(mock);

    await _pump(tester, mock);

    await tester.tap(find.widgetWithText(TextButton, 'Renew'));
    await tester.pumpAndSettle();

    // The confirm sheet renders its own "Renew" button — searching for
    // the sheet's title is the stable signal across confirm-sheet UX
    // variants.
    expect(find.textContaining('Renew certificate'), findsOneWidget);
  });
}
