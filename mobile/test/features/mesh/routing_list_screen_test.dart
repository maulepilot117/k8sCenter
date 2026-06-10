// Widget tests for the routing list.
//
// Coverage:
//   * Empty routes set renders the "no routing rules" guidance.
//   * Mixed Istio + Linkerd rows render.
//   * Mesh filter chip narrows the list.
//   * Partial-failure errors map renders a banner above the list.
//   * Not-detected status falls back to FeatureUnavailableState.mesh().

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/mesh/routing_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const MeshRoutingListScreen(),
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

Map<String, Object?> _emptyRouting() => {
      'data': {
        'status': {
          'detected': 'both',
          'istio': {'installed': true},
          'linkerd': {'installed': true},
        },
        'routes': <Map<String, Object?>>[],
      },
    };

Map<String, Object?> _twoRoutesWithErrors() => {
      'data': {
        'status': {
          'detected': 'both',
          'istio': {'installed': true},
          'linkerd': {'installed': true},
        },
        'routes': [
          {
            'id': 'istio:app:vs:web',
            'mesh': 'istio',
            'kind': 'VirtualService',
            'name': 'web',
            'namespace': 'app',
            'hosts': ['web.example.com'],
            'destinations': [
              {'host': 'web.app.svc.cluster.local', 'weight': 100}
            ],
          },
          {
            'id': 'linkerd:app:sp:api',
            'mesh': 'linkerd',
            'kind': 'ServiceProfile',
            'name': 'api',
            'namespace': 'app',
          },
        ],
        'errors': {
          'istio/AuthorizationPolicy': 'forbidden',
        },
      },
    };

void main() {
  testWidgets('not-detected falls back to FeatureUnavailableState.mesh()',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusNotDetected())
      ..onJson('GET', '/api/v1/mesh/routing', body: _emptyRouting());

    await _pump(tester, mock);

    expect(find.textContaining('service mesh'), findsOneWidget);
  });

  testWidgets('empty routes shows guidance copy', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusBoth())
      ..onJson('GET', '/api/v1/mesh/routing', body: _emptyRouting());

    await _pump(tester, mock);

    expect(find.textContaining('No routing rules'), findsOneWidget);
  });

  testWidgets('renders rows + partial-failure banner', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusBoth())
      ..onJson('GET', '/api/v1/mesh/routing', body: _twoRoutesWithErrors());

    await _pump(tester, mock);

    expect(find.text('web'), findsOneWidget);
    expect(find.text('api'), findsOneWidget);
    expect(find.textContaining('istio/AuthorizationPolicy'), findsOneWidget);
  });

  testWidgets('mesh filter chip narrows visible rows', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusBoth())
      ..onJson('GET', '/api/v1/mesh/routing', body: _twoRoutesWithErrors());

    await _pump(tester, mock);

    final istioChip = find.widgetWithText(ChoiceChip, 'Istio');
    expect(istioChip, findsOneWidget);
    await tester.tap(istioChip);
    await tester.pumpAndSettle();

    // After selecting Istio chip: only the Istio route "web" remains.
    expect(find.text('web'), findsOneWidget);
    expect(find.text('api'), findsNothing);

    // Strengthen: confirm the Istio chip is now selected (checked).
    final istioChipWidget =
        tester.widget<ChoiceChip>(find.widgetWithText(ChoiceChip, 'Istio'));
    expect(istioChipWidget.selected, isTrue,
        reason: 'Istio ChoiceChip should be selected after tap');

    // Linkerd chip is NOT selected.
    final linkerdChipWidget =
        tester.widget<ChoiceChip>(find.widgetWithText(ChoiceChip, 'Linkerd'));
    expect(linkerdChipWidget.selected, isFalse);
  });
}
