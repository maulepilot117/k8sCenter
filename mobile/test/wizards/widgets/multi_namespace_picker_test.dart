// Widget tests for MultiNamespacePicker.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/multi_namespace_picker.dart';

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
      theme: buildKubeTheme('nexus'),
      home: Scaffold(
        body: Padding(padding: const EdgeInsets.all(16), child: child),
      ),
    ),
  );
}

void main() {
  group('MultiNamespacePicker', () {
    testWidgets('renders chips and toggles selection', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/resources/namespaces',
        body: {
          'data': [
            {
              'metadata': {'name': 'production'},
            },
            {
              'metadata': {'name': 'staging'},
            },
          ],
          'metadata': {'total': 2},
        },
      );

      Set<String> captured = {};

      await tester.pumpWidget(_wrap(
        container,
        StatefulBuilder(
          builder: (context, setState) {
            return MultiNamespacePicker(
              clusterId: 'local',
              selected: captured,
              onChanged: (s) => setState(() => captured = s),
              label: 'Included',
            );
          },
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      expect(find.text('production'), findsOneWidget);
      expect(find.text('staging'), findsOneWidget);

      await tester.tap(find.text('production'));
      await tester.pump();
      expect(captured.contains('production'), isTrue);
    });

    testWidgets('toggling a selected chip deselects it', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/resources/namespaces',
        body: {
          'data': [
            {
              'metadata': {'name': 'production'},
            },
          ],
          'metadata': {'total': 1},
        },
      );

      Set<String> captured = {'production'};

      await tester.pumpWidget(_wrap(
        container,
        StatefulBuilder(
          builder: (context, setState) {
            return MultiNamespacePicker(
              clusterId: 'local',
              selected: captured,
              onChanged: (s) => setState(() => captured = s),
            );
          },
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      // Tap the already-selected chip → deselects it.
      await tester.tap(find.text('production'));
      await tester.pump();
      expect(captured.contains('production'), isFalse);
    });

    testWidgets('empty list yields helpful message', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/resources/namespaces',
        body: {
          'data': <Map<String, dynamic>>[],
          'metadata': {'total': 0},
        },
      );

      await tester.pumpWidget(_wrap(
        container,
        const MultiNamespacePicker(
          clusterId: 'local',
          selected: <String>{},
          onChanged: _noOp,
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      expect(find.textContaining('No namespaces'), findsOneWidget);
    });
  });
}

void _noOp(Set<String> _) {}
