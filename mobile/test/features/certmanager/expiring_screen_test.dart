// Widget tests for the expiring certificates surface.
//
// Coverage:
//   * Not detected → FeatureUnavailableState.certManager().
//   * Backend-sorted rows render with severity badges.
//   * Empty list shows the "no expiring" success state.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/certmanager/expiring_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const ExpiringCertificatesScreen(),
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
  });

  testWidgets('renders sorted expiring rows with severity badges',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson(
        'GET',
        '/api/v1/certificates/expiring',
        body: {
          'data': [
            {
              'namespace': 'app',
              'name': 'critical-cert',
              'uid': 'c-uid',
              'issuerName': 'letsencrypt-prod',
              'secretName': 'critical-secret',
              'notAfter': '2026-05-15T00:00:00Z',
              'daysRemaining': 2,
              'severity': 'critical',
            },
            {
              'namespace': 'app',
              'name': 'warning-cert',
              'uid': 'w-uid',
              'issuerName': 'letsencrypt-prod',
              'secretName': 'warning-secret',
              'notAfter': '2026-06-05T00:00:00Z',
              'daysRemaining': 23,
              'severity': 'warning',
            },
          ],
        },
      );

    await _pump(tester, mock);

    expect(find.text('critical-cert'), findsOneWidget);
    expect(find.text('warning-cert'), findsOneWidget);
    expect(find.text('2d'), findsOneWidget);
    expect(find.text('23d'), findsOneWidget);
  });

  testWidgets('empty expiring list shows success state', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/certificates/status', body: _statusDetected())
      ..onJson(
        'GET',
        '/api/v1/certificates/expiring',
        body: {'data': <Map<String, Object?>>[]},
      );

    await _pump(tester, mock);

    expect(
      find.textContaining('No certificates expiring soon'),
      findsOneWidget,
    );
  });
}
