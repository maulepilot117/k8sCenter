// Widget tests for IssuerPicker.
//
// Covers:
//   - Empty namespace renders "Pick a namespace first" hint without
//     hitting the network.
//   - Successful fetch merges Issuer + ClusterIssuer entries into one
//     list, with the kind label visible per row.
//   - Namespaced issuers from other namespaces are filtered out.
//   - Empty result (no issuers anywhere) renders the empty-state copy.
//   - X-Cluster-ID header is set to the pinned clusterId on both list
//     fetches — guards against the cluster-mismatch bug class M3 PR-3c
//     hardened against on the named picker path.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/issuer_picker.dart';

import '../../support/mock_dio_adapter.dart';

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
  group('IssuerPicker', () {
    testWidgets('empty namespace renders hint, no network', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      await tester.pumpWidget(_wrap(
        container,
        IssuerPicker(
          clusterId: 'local',
          namespace: '',
          selected: null,
          onChanged: (_) {},
          label: 'Issuer',
        ),
      ));
      await tester.pump();

      expect(find.textContaining('Pick a namespace'), findsOneWidget);
      expect(mock.requests, isEmpty);
    });

    testWidgets('merges Issuer + ClusterIssuer entries, filters namespace',
        (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/certificates/issuers',
        body: {
          'data': [
            {'name': 'app-issuer', 'namespace': 'app', 'kind': 'Issuer'},
            {'name': 'other-issuer', 'namespace': 'other', 'kind': 'Issuer'},
          ],
        },
      );
      mock.onJson(
        'GET',
        '/api/v1/certificates/clusterissuers',
        body: {
          'data': [
            {'name': 'letsencrypt-prod', 'kind': 'ClusterIssuer'},
          ],
        },
      );

      await tester.pumpWidget(_wrap(
        container,
        IssuerPicker(
          clusterId: 'local',
          namespace: 'app',
          selected: null,
          onChanged: (_) {},
          label: 'Issuer',
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      // Open the dropdown to see entries.
      await tester.tap(find.byType(DropdownButtonFormField<IssuerSelection>));
      await tester.pumpAndSettle();

      expect(find.text('app-issuer'), findsOneWidget);
      expect(find.text('letsencrypt-prod'), findsOneWidget);
      // The other-namespace issuer is filtered out client-side.
      expect(find.text('other-issuer'), findsNothing);
    });

    testWidgets('empty issuers across both lists shows empty-state copy',
        (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/certificates/issuers',
          body: {'data': <Map<String, dynamic>>[]});
      mock.onJson(
        'GET',
        '/api/v1/certificates/clusterissuers',
        body: {'data': <Map<String, dynamic>>[]},
      );

      await tester.pumpWidget(_wrap(
        container,
        IssuerPicker(
          clusterId: 'local',
          namespace: 'app',
          selected: null,
          onChanged: (_) {},
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      expect(find.textContaining('No Issuers'), findsOneWidget);
    });

    testWidgets('X-Cluster-ID header pins both fetches to the wizard cluster',
        (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/certificates/issuers',
          body: {'data': <Map<String, dynamic>>[]});
      mock.onJson(
        'GET',
        '/api/v1/certificates/clusterissuers',
        body: {'data': <Map<String, dynamic>>[]},
      );

      await tester.pumpWidget(_wrap(
        container,
        IssuerPicker(
          clusterId: 'pinned-A',
          namespace: 'app',
          selected: null,
          onChanged: (_) {},
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      expect(mock.requests, hasLength(2));
      for (final req in mock.requests) {
        expect(req.headers['X-Cluster-ID'], 'pinned-A');
      }
    });
  });
}
