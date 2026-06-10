// Widget tests for the cert-manager certificates list.
//
// Coverage:
//   * Not-detected status → FeatureUnavailableState.certManager().
//   * Mixed status rows render with status pills.
//   * Filter chip narrows the list (Expiring / Failed).
//   * `?status=expiring` initialStatusFilter pre-selects the chip.
//   * Search filter narrows by name / namespace / issuer.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/certmanager/certificates_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(
  WidgetTester tester,
  MockDioAdapter mock, {
  String? initialStatusFilter,
}) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => CertificatesListScreen(
          initialStatusFilter: initialStatusFilter,
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
        theme: buildKubeTheme('liquid-glass'),
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
      'data': {
        'detected': true,
        'namespace': 'cert-manager',
        'version': '1.14.0',
        'lastChecked': '2026-05-12T10:00:00Z',
      },
    };

Map<String, Object?> _statusNotDetected() => {
      'data': {'detected': false},
    };

Map<String, Object?> _certsBody() => {
      'data': [
        {
          'name': 'web-tls',
          'namespace': 'app',
          'status': 'Ready',
          'issuerRef': {'name': 'letsencrypt-prod', 'kind': 'ClusterIssuer'},
          'secretName': 'web-tls-secret',
          'dnsNames': ['web.example.com'],
          'daysRemaining': 45,
          'warningThresholdDays': 30,
          'criticalThresholdDays': 7,
          'uid': 'cert-1',
        },
        {
          'name': 'api-tls',
          'namespace': 'app',
          'status': 'Expiring',
          'issuerRef': {'name': 'letsencrypt-prod', 'kind': 'ClusterIssuer'},
          'secretName': 'api-tls-secret',
          'daysRemaining': 5,
          'warningThresholdDays': 30,
          'criticalThresholdDays': 7,
          'uid': 'cert-2',
        },
        {
          'name': 'broken-tls',
          'namespace': 'kube-system',
          'status': 'Failed',
          'issuerRef': {'name': 'self-signed', 'kind': 'Issuer'},
          'secretName': 'broken-secret',
          'reason': 'IssuerNotReady',
          'uid': 'cert-3',
        },
      ],
    };

void main() {
  testWidgets('detected=false renders FeatureUnavailableState.certManager()',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusNotDetected());

    await _pump(tester, mock);

    expect(find.textContaining('cert-manager'), findsWidgets);
    expect(find.text('web-tls'), findsNothing);
  });

  testWidgets('renders certificate rows with status pills and expiry badge',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson('GET', '/api/v1/certificates/certificates', body: _certsBody());

    await _pump(tester, mock);

    expect(find.text('web-tls'), findsOneWidget);
    expect(find.text('api-tls'), findsOneWidget);
    expect(find.text('broken-tls'), findsOneWidget);
    // Status pills:
    expect(find.text('Ready'), findsOneWidget);
    expect(find.text('Expiring'), findsWidgets); // pill + filter-chip label
    expect(find.text('Failed'), findsWidgets); // pill + filter-chip label
    // Expiry badges:
    expect(find.text('45d'), findsOneWidget);
    expect(find.text('5d left'), findsOneWidget);
  });

  testWidgets('filter chip narrows to Expiring + Expired only', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson('GET', '/api/v1/certificates/certificates', body: _certsBody());

    await _pump(tester, mock);

    // Pre-tap state: all three rows visible.
    expect(find.text('web-tls'), findsOneWidget);
    expect(find.text('broken-tls'), findsOneWidget);

    // Tap the "Expiring" filter chip.
    await tester.tap(find.widgetWithText(ChoiceChip, 'Expiring'));
    await tester.pumpAndSettle();

    // Only api-tls (Expiring) remains.
    expect(find.text('api-tls'), findsOneWidget);
    expect(find.text('web-tls'), findsNothing);
    expect(find.text('broken-tls'), findsNothing);
  });

  testWidgets('initialStatusFilter pre-selects Expiring on mount',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson('GET', '/api/v1/certificates/certificates', body: _certsBody());

    await _pump(tester, mock, initialStatusFilter: 'expiring');

    // Pre-filtered: only api-tls (Expiring) visible.
    expect(find.text('api-tls'), findsOneWidget);
    expect(find.text('web-tls'), findsNothing);
  });

  testWidgets('search filter narrows by namespace token', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson('GET', '/api/v1/certificates/certificates', body: _certsBody());

    await _pump(tester, mock);

    await tester.enterText(find.byType(TextField), 'kube-system');
    await tester.pumpAndSettle();

    expect(find.text('broken-tls'), findsOneWidget);
    expect(find.text('web-tls'), findsNothing);
    expect(find.text('api-tls'), findsNothing);
  });

  testWidgets('search filter narrows by issuer name token', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson('GET', '/api/v1/certificates/certificates', body: _certsBody());

    await _pump(tester, mock);

    await tester.enterText(find.byType(TextField), 'self-signed');
    await tester.pumpAndSettle();

    // Only broken-tls uses self-signed.
    expect(find.text('broken-tls'), findsOneWidget);
    expect(find.text('web-tls'), findsNothing);
  });

  testWidgets('empty cert list shows "no certificates" empty state',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson(
        'GET',
        '/api/v1/certificates/certificates',
        body: {'data': <Map<String, Object?>>[]},
      );

    await _pump(tester, mock);

    expect(find.textContaining('No certificates'), findsOneWidget);
  });
}
