// Widget tests for StorePicker.
//
// Covers:
//   - Empty namespace renders "Pick a namespace first" hint without hitting network.
//   - Loading state renders LinearProgressIndicator while storeListProvider loads.
//   - Empty data from both endpoints renders "No SecretStores" hint.
//   - Combined list renders namespaced stores with account_tree_outlined icon and
//     cluster stores with public icon; provider hint appears as trailing label.
//   - StoreListKey equality: same values are equal; different namespace is not equal.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/store_picker.dart';

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
  group('StorePicker widget', () {
    testWidgets('empty namespace renders hint, no network calls', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      await tester.pumpWidget(_wrap(
        container,
        StorePicker(
          clusterId: 'local',
          namespace: '',
          selected: null,
          onChanged: (_) {},
        ),
      ));
      await tester.pump();

      expect(find.textContaining('Pick a namespace first'), findsOneWidget);
      expect(mock.requests, isEmpty);
    });

    testWidgets('whitespace-only namespace renders hint, no network calls',
        (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      await tester.pumpWidget(_wrap(
        container,
        StorePicker(
          clusterId: 'local',
          namespace: '   ',
          selected: null,
          onChanged: (_) {},
        ),
      ));
      await tester.pump();

      expect(find.textContaining('Pick a namespace first'), findsOneWidget);
      expect(mock.requests, isEmpty);
    });

    testWidgets('renders LinearProgressIndicator while loading', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      // The mock responds synchronously in the fake-async environment, so the
      // loading frame is visible after pumpWidget but before the pump that
      // processes microtasks from the Future.wait. We assert immediately after
      // pumpWidget (no extra pump) before the FutureProvider has a chance to
      // complete.
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores',
        body: {'data': <Map<String, dynamic>>[]},
      );
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/clusterstores',
        body: {'data': <Map<String, dynamic>>[]},
      );

      await tester.pumpWidget(_wrap(
        container,
        StorePicker(
          clusterId: 'local',
          namespace: 'app',
          selected: null,
          onChanged: (_) {},
        ),
      ));

      // Loading indicator is visible on the first frame before data resolves.
      expect(find.byType(LinearProgressIndicator), findsOneWidget);

      // Drain the pending futures so no timer leaks out of the test.
      await tester.pumpAndSettle();
    });

    testWidgets('empty data from both endpoints renders no-stores hint',
        (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores',
        body: {'data': <Map<String, dynamic>>[]},
      );
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/clusterstores',
        body: {'data': <Map<String, dynamic>>[]},
      );

      await tester.pumpWidget(_wrap(
        container,
        StorePicker(
          clusterId: 'local',
          namespace: 'app',
          selected: null,
          onChanged: (_) {},
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      expect(find.textContaining('No SecretStores'), findsOneWidget);
    });

    testWidgets(
        'combined list: namespaced store shows account_tree_outlined icon, '
        'cluster store shows public icon, provider hint visible', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores',
        body: {
          'data': [
            {'name': 'vault-ns', 'provider': 'vault'},
          ],
        },
      );
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/clusterstores',
        body: {
          'data': [
            {'name': 'vault-cluster', 'provider': 'vault'},
          ],
        },
      );

      await tester.pumpWidget(_wrap(
        container,
        StorePicker(
          clusterId: 'local',
          namespace: 'app',
          selected: null,
          onChanged: (_) {},
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      // Open the dropdown to see items.
      await tester.tap(find.byType(DropdownButtonFormField<StoreSelection>));
      await tester.pumpAndSettle();

      expect(find.text('vault-ns'), findsOneWidget);
      expect(find.text('vault-cluster'), findsOneWidget);

      // Namespaced store uses account_tree_outlined icon.
      expect(find.byIcon(Icons.account_tree_outlined), findsWidgets);
      // Cluster store uses public icon.
      expect(find.byIcon(Icons.public), findsWidgets);

      // Provider hint text: "SecretStore · vault" and "ClusterSecretStore · vault".
      expect(find.textContaining('SecretStore · vault'), findsWidgets);
    });

    testWidgets('onChanged fires with correct StoreSelection on tap', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      StoreSelection? selected;

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores',
        body: {
          'data': [
            {'name': 'my-store', 'provider': 'aws'},
          ],
        },
      );
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/clusterstores',
        body: {'data': <Map<String, dynamic>>[]},
      );

      await tester.pumpWidget(_wrap(
        container,
        StatefulBuilder(
          builder: (ctx, setState) => StorePicker(
            clusterId: 'local',
            namespace: 'app',
            selected: selected,
            onChanged: (s) => setState(() => selected = s),
          ),
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      await tester.tap(find.byType(DropdownButtonFormField<StoreSelection>));
      await tester.pumpAndSettle();
      await tester.tap(find.text('my-store').last);
      await tester.pumpAndSettle();

      expect(selected, isNotNull);
      expect(selected!.name, 'my-store');
      expect(selected!.kind, 'SecretStore');
    });
  });

  group('StoreListKey equality', () {
    test('equal when clusterId and namespace match', () {
      expect(
        StoreListKey(clusterId: 'a', namespace: 'b'),
        equals(StoreListKey(clusterId: 'a', namespace: 'b')),
      );
    });

    test('not equal when namespace differs', () {
      expect(
        StoreListKey(clusterId: 'a', namespace: 'b'),
        isNot(equals(StoreListKey(clusterId: 'a', namespace: 'c'))),
      );
    });

    test('not equal when clusterId differs', () {
      expect(
        StoreListKey(clusterId: 'a', namespace: 'b'),
        isNot(equals(StoreListKey(clusterId: 'x', namespace: 'b'))),
      );
    });

    test('hashCode matches for equal keys', () {
      final k1 = StoreListKey(clusterId: 'local', namespace: 'app');
      final k2 = StoreListKey(clusterId: 'local', namespace: 'app');
      expect(k1.hashCode, k2.hashCode);
    });
  });
}
