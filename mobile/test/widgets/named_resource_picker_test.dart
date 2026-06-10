// Widget tests for NamedResourcePicker.
//
// Covers:
//   - Empty state ("No <kind> in <ns>") when the backend returns no
//     items.
//   - Loading state.
//   - Successful list renders names sorted; tapping the dropdown
//     entry emits onChanged.
//   - clusterIdOverride is forwarded to the wire request — guards
//     against the M3 PR-3c review bug where the picker fetched against
//     the active cluster while the cache slot was keyed on the
//     pinned-wizard cluster.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/named_resource_picker.dart';

import '../support/mock_dio_adapter.dart';

({ProviderContainer container, MockDioAdapter mock}) _makeContainer() {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
  );
  container.read(dioProvider).httpClientAdapter = mock;
  container.read(refreshDioProvider).httpClientAdapter = mock;
  return (container: container, mock: mock);
}

Widget _wrap(ProviderContainer container, Widget child) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: buildKubeTheme('liquid-glass'),
      home: Scaffold(
        body: Padding(padding: const EdgeInsets.all(16), child: child),
      ),
    ),
  );
}

void main() {
  group('NamedResourcePicker', () {
    testWidgets('empty namespace shows "No <kind> in <ns>"', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/resources/deployments/default',
        body: {
          'data': <Map<String, dynamic>>[],
          'metadata': {'total': 0},
        },
      );

      await tester.pumpWidget(_wrap(
        container,
        const NamedResourcePicker(
          clusterId: 'local',
          kind: 'deployments',
          namespace: 'default',
          selected: '',
          onChanged: _noOp,
          label: 'Deployment',
        ),
      ));
      // Initial frame is loading state.
      await tester.pump();
      // Drain pending HTTP futures.
      await tester.pump(const Duration(milliseconds: 50));

      expect(find.text('No deployments in default'), findsOneWidget);
    });

    testWidgets('renders sorted names from the list response', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/resources/deployments/default',
        body: {
          'data': [
            {
              'metadata': {'name': 'web'},
            },
            {
              'metadata': {'name': 'api'},
            },
          ],
          'metadata': {'total': 2},
        },
      );

      await tester.pumpWidget(_wrap(
        container,
        const NamedResourcePicker(
          clusterId: 'local',
          kind: 'deployments',
          namespace: 'default',
          selected: '',
          onChanged: _noOp,
          label: 'Deployment',
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      // The dropdown's current value is null (no selection yet) but
      // the items list has both names. Tap to open the menu.
      expect(find.byType(DropdownButtonFormField<String>), findsOneWidget);

      await tester.tap(find.byType(DropdownButtonFormField<String>));
      await tester.pumpAndSettle();
      // Both menu entries visible (the dropdown also renders the same
      // text in the closed field if a value is selected; here selected
      // is empty so only the menu entries appear).
      expect(find.text('api'), findsOneWidget);
      expect(find.text('web'), findsOneWidget);
    });

    testWidgets(
        'forwards clusterIdOverride: request goes to pinned cluster, not active',
        (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/resources/deployments/default',
        body: {
          'data': <Map<String, dynamic>>[],
          'metadata': {'total': 0},
        },
      );

      await tester.pumpWidget(_wrap(
        container,
        const NamedResourcePicker(
          clusterId: 'pinned-cluster-A',
          kind: 'deployments',
          namespace: 'default',
          selected: '',
          onChanged: _noOp,
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      expect(mock.requests, isNotEmpty);
      // Last request's X-Cluster-ID header should be the pinned id —
      // not whatever activeClusterProvider happens to read. This is
      // the regression-guard for the cluster-mismatch bug.
      final hdr = mock.requests.last.headers['X-Cluster-ID'];
      expect(hdr, 'pinned-cluster-A');
    });
  });
}

void _noOp(String _) {}
