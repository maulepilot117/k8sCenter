// Widget tests for the mTLS posture screen.
//
// Coverage:
//   * not-detected → FeatureUnavailableState.mesh()
//   * no namespace selected → renders _NamespacePrompt "Choose a namespace"
//   * happy path with 3 workloads (one mixed-state, one inactive, one with
//     workloadKindConfident: false) → 3 rows render, asterisk + tooltip
//     visible on the unconfident row
//   * partial errors map → MeshErrorsBanner renders above the workload list
//   * namespace-list fails (5xx for resourceListProvider) → the manual-entry
//     TextField renders so the user can type a namespace manually

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/mesh/mtls_posture_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const MeshMtlsPostureScreen(),
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

Map<String, Object?> _statusNotDetected() => {
      'data': {
        'status': {
          'detected': '',
          'istio': {'installed': false},
          'linkerd': {'installed': false},
        },
      },
    };

Map<String, Object?> _statusIstioOnly() => {
      'data': {
        'status': {
          'detected': 'istio',
          'istio': {'installed': true},
          'linkerd': {'installed': false},
        },
      },
    };

Map<String, Object?> _emptyNamespaceList() => {
      'data': <Map<String, Object?>>[],
      'metadata': {'total': 0},
    };

Map<String, Object?> _namespaceListWithApp() => {
      'data': [
        {
          'metadata': {'name': 'app'},
        },
      ],
      'metadata': {'total': 1},
    };

Map<String, Object?> _postureThreeWorkloads() => {
      'data': {
        'status': {
          'detected': 'istio',
          'istio': {'installed': true},
          'linkerd': {'installed': false},
        },
        'workloads': [
          {
            'namespace': 'app',
            'workload': 'web',
            'workloadKind': 'Deployment',
            'mesh': 'istio',
            'state': 'mixed',
            'source': 'policy',
            'workloadKindConfident': true,
          },
          {
            'namespace': 'app',
            'workload': 'legacy-api',
            'workloadKind': 'Deployment',
            'mesh': 'istio',
            'state': 'inactive',
            'source': 'default',
            'workloadKindConfident': true,
          },
          {
            'namespace': 'app',
            'workload': 'orphan-abc',
            'workloadKind': 'Deployment',
            'mesh': 'istio',
            'state': 'active',
            'source': 'metric',
            // workloadKindConfident omitted → defaults to false (finding #8)
          },
        ],
        'errors': <String, Object?>{},
      },
    };

Map<String, Object?> _postureWithErrors() => {
      'data': {
        'status': {
          'detected': 'istio',
          'istio': {'installed': true},
          'linkerd': {'installed': false},
        },
        'workloads': [
          {
            'namespace': 'app',
            'workload': 'web',
            'workloadKind': 'Deployment',
            'mesh': 'istio',
            'state': 'active',
            'source': 'policy',
            'workloadKindConfident': true,
          },
        ],
        'errors': {
          'pods': 'insufficient RBAC to list pods',
          'truncated': 'capped at 500 workloads',
        },
      },
    };

void main() {
  testWidgets('not-detected renders FeatureUnavailableState.mesh()',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusNotDetected())
      ..onJson('GET', '/api/v1/resources/namespaces',
          body: _emptyNamespaceList());

    await _pump(tester, mock);

    expect(find.textContaining('service mesh'), findsOneWidget);
    expect(find.text('Choose a namespace'), findsNothing);
  });

  testWidgets('no namespace selected renders namespace prompt', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusIstioOnly())
      ..onJson('GET', '/api/v1/resources/namespaces',
          body: _namespaceListWithApp());

    await _pump(tester, mock);

    expect(find.text('Choose a namespace'), findsOneWidget);
    // mTLS fetch should NOT have fired yet.
    final mtlsRequests =
        mock.requests.where((r) => r.path.contains('/api/v1/mesh/mtls'));
    expect(mtlsRequests, isEmpty);
  });

  testWidgets('happy path with 3 workloads renders rows + asterisk on unconfident',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusIstioOnly())
      ..onJson('GET', '/api/v1/resources/namespaces',
          body: _namespaceListWithApp())
      ..onJson('GET', '/api/v1/mesh/mtls', body: _postureThreeWorkloads());

    await _pump(tester, mock);

    // Select the namespace from the dropdown.
    await tester.tap(find.byType(DropdownButton<String?>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('app').last);
    await tester.pumpAndSettle(const Duration(milliseconds: 300));

    // Three workload names are visible.
    expect(find.text('web'), findsOneWidget);
    expect(find.text('legacy-api'), findsOneWidget);
    expect(find.text('orphan-abc'), findsOneWidget);

    // Asterisk visible on the unconfident row (orphan-abc).
    expect(find.text('*'), findsOneWidget);
  });

  testWidgets('partial errors map renders MeshErrorsBanner', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusIstioOnly())
      ..onJson('GET', '/api/v1/resources/namespaces',
          body: _namespaceListWithApp())
      ..onJson('GET', '/api/v1/mesh/mtls', body: _postureWithErrors());

    await _pump(tester, mock);

    await tester.tap(find.byType(DropdownButton<String?>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('app').last);
    await tester.pumpAndSettle(const Duration(milliseconds: 300));

    // Error banner (pods key + message) and warn banner (truncated) both render.
    expect(find.textContaining('pods'), findsAtLeastNWidgets(1));
    expect(find.textContaining('truncated'), findsOneWidget);
  });

  testWidgets(
      'namespace-list 5xx renders manual-entry TextField + Retry button',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/mesh/status', body: _statusIstioOnly())
      ..on(
          'GET',
          '/api/v1/resources/namespaces',
          (_) => ResponseBody.fromString(
                '{"error":{"code":503,"message":"upstream unavailable"}}',
                503,
                headers: {
                  'content-type': ['application/json'],
                },
              ));

    await _pump(tester, mock);

    // After the error the manual-entry TextField replaces the dropdown.
    expect(find.byType(TextField), findsOneWidget);
    // Retry button present.
    expect(find.byIcon(Icons.refresh), findsOneWidget);
  });
}
