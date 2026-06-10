// Widget tests for MeshPoliciesListScreen (#259).
//
// Coverage mirrors routing_list_screen_test.dart:
//   * Empty policies set renders guidance copy.
//   * Mixed Istio + Linkerd rows render with the right status pills.
//   * Mesh filter chip narrows the list.
//   * Partial-failure errors map renders a banner above the list.
//   * Not-detected status falls back to FeatureUnavailableState.mesh().

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/mesh/policies_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const MeshPoliciesListScreen(),
      ),
    ],
  );
  await tester.pumpWidget(
    ProviderScope(
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
    ),
  );
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

Map<String, Object?> _statusBoth() => {
  'data': {
    'status': {
      'detected': 'both',
      'istio': {'installed': true},
      'linkerd': {'installed': true},
    },
  },
};

Map<String, Object?> _statusNotDetected() => {
  'data': {
    'status': {
      'detected': '',
      'istio': {'installed': false},
      'linkerd': {'installed': false},
    },
  },
};

Map<String, Object?> _emptyPolicies() => {
  'data': {
    'status': {
      'detected': 'both',
      'istio': {'installed': true},
      'linkerd': {'installed': true},
    },
    'policies': <Map<String, Object?>>[],
  },
};

Map<String, Object?> _twoPoliciesWithErrors() => {
  'data': {
    'status': {
      'detected': 'both',
      'istio': {'installed': true},
      'linkerd': {'installed': true},
    },
    'policies': [
      {
        'id': 'istio:app:pa:strict-mtls',
        'mesh': 'istio',
        'kind': 'PeerAuthentication',
        'name': 'strict-mtls',
        'namespace': 'app',
        'mtlsMode': 'STRICT',
        'selector': 'app=web',
        'ruleCount': 1,
      },
      {
        'id': 'linkerd:app:mtls:require-mtls',
        'mesh': 'linkerd',
        'kind': 'MeshTLSAuthentication',
        'name': 'require-mtls',
        'namespace': 'app',
        'ruleCount': 0,
      },
    ],
    'errors': {'istio/AuthorizationPolicy': 'forbidden'},
  },
};

void main() {
  testWidgets('not-detected falls back to FeatureUnavailableState.mesh()', (
    tester,
  ) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusNotDetected())
      ..onJson('GET', '/api/v1/mesh/policies', body: _emptyPolicies());

    await _pump(tester, mock);

    expect(find.textContaining('service mesh'), findsOneWidget);
  });

  testWidgets('empty policies shows guidance copy', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusBoth())
      ..onJson('GET', '/api/v1/mesh/policies', body: _emptyPolicies());

    await _pump(tester, mock);

    expect(find.textContaining('No mesh policies'), findsOneWidget);
  });

  testWidgets('renders rows + partial-failure banner', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusBoth())
      ..onJson('GET', '/api/v1/mesh/policies', body: _twoPoliciesWithErrors());

    await _pump(tester, mock);

    // Policy names visible.
    expect(find.text('strict-mtls'), findsOneWidget);
    expect(find.text('require-mtls'), findsOneWidget);
    // Kind + namespace combined line.
    expect(find.textContaining('PeerAuthentication'), findsOneWidget);
    expect(find.textContaining('MeshTLSAuthentication'), findsOneWidget);
    // mTLS mode pill from the PeerAuthentication row.
    expect(find.text('STRICT'), findsOneWidget);
    // Selector preview from the PeerAuthentication row.
    expect(find.text('app=web'), findsOneWidget);
    // Rule count badge: only the PA row has ruleCount=1.
    expect(find.textContaining('1 rule'), findsOneWidget);
    // Partial-failure banner.
    expect(find.textContaining('istio/AuthorizationPolicy'), findsOneWidget);
  });

  testWidgets('mesh filter chip narrows visible rows', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusBoth())
      ..onJson('GET', '/api/v1/mesh/policies', body: _twoPoliciesWithErrors());

    await _pump(tester, mock);

    final istioChip = find.widgetWithText(ChoiceChip, 'Istio');
    expect(istioChip, findsOneWidget);
    await tester.tap(istioChip);
    await tester.pumpAndSettle();

    // After selecting Istio chip: only the Istio policy remains.
    expect(find.text('strict-mtls'), findsOneWidget);
    expect(find.text('require-mtls'), findsNothing);

    final istioChipWidget = tester.widget<ChoiceChip>(
      find.widgetWithText(ChoiceChip, 'Istio'),
    );
    expect(
      istioChipWidget.selected,
      isTrue,
      reason: 'Istio ChoiceChip should be selected after tap',
    );

    final linkerdChipWidget = tester.widget<ChoiceChip>(
      find.widgetWithText(ChoiceChip, 'Linkerd'),
    );
    expect(linkerdChipWidget.selected, isFalse);
  });
}
