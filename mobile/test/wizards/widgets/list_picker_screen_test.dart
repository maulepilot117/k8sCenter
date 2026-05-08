// Widget tests for ListPickerScreen.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/list_picker_screen.dart';

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
      home: Scaffold(body: child),
    ),
  );
}

void main() {
  group('ListPickerScreen', () {
    testWidgets('empty state renders title + message', (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/resources/volumesnapshots/app',
        body: {
          'data': <Map<String, dynamic>>[],
          'metadata': {'total': 0},
        },
      );

      await tester.pumpWidget(_wrap(
        container,
        const ListPickerScreen(
          clusterId: 'local',
          kind: 'volumesnapshots',
          namespace: 'app',
          selectedName: '',
          onChanged: _noOp,
          emptyTitle: 'No snapshots in this namespace',
          emptyMessage: 'Create one with the Snapshot wizard.',
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      expect(find.text('No snapshots in this namespace'), findsOneWidget);
      expect(
          find.text('Create one with the Snapshot wizard.'), findsOneWidget);
    });

    testWidgets('tap on item dispatches onChanged with that name',
        (tester) async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/resources/volumesnapshots/app',
        body: {
          'data': [
            {
              'metadata': {'name': 'snap-1'},
            },
            {
              'metadata': {'name': 'snap-2'},
            },
          ],
          'metadata': {'total': 2},
        },
      );

      String captured = '';

      await tester.pumpWidget(_wrap(
        container,
        StatefulBuilder(
          builder: (context, setState) {
            return ListPickerScreen(
              clusterId: 'local',
              kind: 'volumesnapshots',
              namespace: 'app',
              selectedName: captured,
              onChanged: (v) => setState(() => captured = v),
            );
          },
        ),
      ));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      await tester.tap(find.text('snap-2'));
      await tester.pump();
      expect(captured, 'snap-2');
    });
  });
}

void _noOp(String _) {}
