// RestoreSnapshotWizardController tests. RestoreSnapshot is a UX
// wrapper over the `pvc` wizard type — verify the body carries
// dataSource and that missing snapshot fails validateLocally.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/restore_snapshot/restore_snapshot_wizard_controller.dart';
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
  return container.listen(restoreSnapshotWizardProvider(_key), (_, _) {});
}

void main() {
  group('RestoreSnapshotWizardController', () {
    test('wizardType is pvc — wire stays compatible', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c =
          container.read(restoreSnapshotWizardProvider(_key).notifier);
      expect(c.wizardType, 'pvc');
    });

    test('happy path: body carries dataSource pointing at the snapshot',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c =
          container.read(restoreSnapshotWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const RestoreSnapshotForm(
        name: 'data-restored',
        namespace: 'app',
        sourceSnapshot: 'data-snap-2026-05-08',
        storageClassName: 'standard',
        sizeValue: '5',
        sizeUnit: 'Gi',
        accessMode: 'ReadWriteOnce',
      ));

      expect(body['name'], 'data-restored');
      expect(body['size'], '5Gi');
      final ds = body['dataSource'] as Map<String, dynamic>;
      expect(ds['name'], 'data-snap-2026-05-08');
      expect(ds['kind'], 'VolumeSnapshot');
      expect(ds['apiGroup'], 'snapshot.storage.k8s.io');
    });

    test('missing source snapshot fails validateLocally', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(restoreSnapshotWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'd',
            namespace: 'a',
            storageClassName: 's',
          ));
      await notifier.next();

      final state = container.read(restoreSnapshotWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['dataSource.name'], isNotNull);
    });
  });
}
