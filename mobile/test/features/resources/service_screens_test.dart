// Lifecycle invariants for ServiceDetailScreen — issue #260.
//
// The Golden Signals extraTab is gated on `meshStatusProvider`. Without
// the latch added in this PR, a transient 5xx on `/v1/mesh/status` flips
// `MeshStatus` to `empty`, the tab vanishes mid-session, and the user's
// active selection resets. This test pumps an installed mesh, then
// invalidates the provider so the mocked endpoint serves an empty
// response, and asserts the tab survives.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/mesh_repository.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/resources/service_screens.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

void main() {
  testWidgets(
    'Golden signals tab persists when mesh status transiently flips to empty',
    (tester) async {
      final mock = MockDioAdapter();

      // Service detail GET — returns once and is cached.
      mock.onJson(
        'GET',
        '/api/v1/resources/services/default/my-svc',
        body: {
          'data': {
            'metadata': {
              'name': 'my-svc',
              'namespace': 'default',
              'uid': 'uid-1',
            },
            'spec': {
              'type': 'ClusterIP',
              'clusterIP': '10.0.0.1',
              'ports': [
                {'name': 'http', 'port': 80, 'protocol': 'TCP'},
              ],
            },
          },
        },
      );

      // First mesh status: Istio installed → tab should appear.
      mock.onJson(
        'GET',
        '/api/v1/mesh/status',
        body: {
          'data': {
            'status': {
              'detected': 'istio',
              'istio': {'installed': true, 'version': '1.20'},
              'linkerd': {'installed': false},
              'lastChecked': '2026-05-16T00:00:00Z',
            },
          },
        },
      );
      // Second mesh status (after invalidate): empty → simulates 5xx
      // recovery returning MeshStatus.empty from the repository.
      mock.onJson(
        'GET',
        '/api/v1/mesh/status',
        body: {
          'data': {
            'status': {
              'detected': '',
              'istio': {'installed': false},
              'linkerd': {'installed': false},
              'lastChecked': '',
            },
          },
        },
      );

      final container = ProviderContainer(
        overrides: [
          backendUrlProvider.overrideWithValue('http://test'),
          secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        ],
      );
      addTearDown(container.dispose);
      container.read(dioProvider).httpClientAdapter = mock;
      container.read(refreshDioProvider).httpClientAdapter = mock;

      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            theme: buildKubeTheme('liquid-glass'),
            home: const ServiceDetailScreen(
              namespace: 'default',
              name: 'my-svc',
            ),
          ),
        ),
      );

      // Let the resource GET + mesh status complete.
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));
      await tester.pump(const Duration(milliseconds: 100));

      expect(
        find.text('Golden signals'),
        findsOneWidget,
        reason: 'Tab should appear after first installed mesh status',
      );

      // Force a refetch — the next mocked response is MeshStatus.empty,
      // simulating the transient backend hiccup described in #260.
      container.invalidate(meshStatusProvider('local'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));
      await tester.pump(const Duration(milliseconds: 100));

      expect(
        find.text('Golden signals'),
        findsOneWidget,
        reason:
            'Tab must survive transient mesh status empty — '
            'a passing 5xx should not vanish an operator\'s active tab.',
      );

      // Sanity: verify both mesh status calls actually fired.
      final meshCalls = mock.requests
          .where((r) => r.path == '/api/v1/mesh/status')
          .toList();
      expect(
        meshCalls.length,
        greaterThanOrEqualTo(2),
        reason:
            'Both initial and post-invalidate mesh status calls should '
            'have hit the mock adapter',
      );
    },
  );

  testWidgets(
    'Golden signals tab is absent when mesh has never been observed installed',
    (tester) async {
      final mock = MockDioAdapter();

      mock.onJson(
        'GET',
        '/api/v1/resources/services/default/my-svc',
        body: {
          'data': {
            'metadata': {
              'name': 'my-svc',
              'namespace': 'default',
              'uid': 'uid-1',
            },
            'spec': {
              'type': 'ClusterIP',
              'clusterIP': '10.0.0.1',
              'ports': <dynamic>[],
            },
          },
        },
      );

      // Mesh status reports empty from the start — no mesh installed.
      mock.onJson(
        'GET',
        '/api/v1/mesh/status',
        body: {
          'data': {
            'status': {
              'detected': '',
              'istio': {'installed': false},
              'linkerd': {'installed': false},
              'lastChecked': '',
            },
          },
        },
      );

      final container = ProviderContainer(
        overrides: [
          backendUrlProvider.overrideWithValue('http://test'),
          secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        ],
      );
      addTearDown(container.dispose);
      container.read(dioProvider).httpClientAdapter = mock;
      container.read(refreshDioProvider).httpClientAdapter = mock;

      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            theme: buildKubeTheme('liquid-glass'),
            home: const ServiceDetailScreen(
              namespace: 'default',
              name: 'my-svc',
            ),
          ),
        ),
      );

      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));
      await tester.pump(const Duration(milliseconds: 100));

      expect(
        find.text('Golden signals'),
        findsNothing,
        reason: 'Tab should not appear when no mesh has ever been installed',
      );
    },
  );
}
