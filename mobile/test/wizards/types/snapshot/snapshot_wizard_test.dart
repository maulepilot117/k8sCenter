// SnapshotWizardController tests.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/snapshot/snapshot_wizard_controller.dart';
import 'package:kubecenter/wizards/wizard_controller.dart';

import '../../../support/mock_dio_adapter.dart';

const _key = WizardKey(clusterId: 'local');

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

ProviderSubscription _keepAlive(ProviderContainer container) {
  return container.listen(snapshotWizardProvider(_key), (_, _) {});
}

void main() {
  group('SnapshotWizardController', () {
    test('happy path: snapshot of data PVC body', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(snapshotWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const SnapshotForm(
        name: 'data-snap',
        namespace: 'app',
        sourcePVC: 'data',
        volumeSnapshotClassName: 'csi-hostpath',
      ));

      expect(body['name'], 'data-snap');
      expect(body['namespace'], 'app');
      expect(body['sourcePVC'], 'data');
      expect(body['volumeSnapshotClassName'], 'csi-hostpath');
    });

    test('empty volumeSnapshotClassName is omitted from body', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(snapshotWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const SnapshotForm(
        name: 'data-snap',
        namespace: 'app',
        sourcePVC: 'data',
      ));
      expect(body.containsKey('volumeSnapshotClassName'), false);
    });

    test('errorRouter routes known field paths to step 0; unknown fall '
        'through to unrouted', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(snapshotWizardProvider(_key).notifier);
      expect(c.errorRouter('name'), 0);
      expect(c.errorRouter('namespace'), 0);
      expect(c.errorRouter('sourcePVC'), 0);
      expect(c.errorRouter('volumeSnapshotClassName'), 0);
      expect(c.errorRouter('mystery'), null);
    });

    test('missing source PVC fails validateLocally', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(snapshotWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'x', namespace: 'y'));
      await notifier.next();

      final state = container.read(snapshotWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['sourcePVC'], isNotNull);
    });
  });
}
