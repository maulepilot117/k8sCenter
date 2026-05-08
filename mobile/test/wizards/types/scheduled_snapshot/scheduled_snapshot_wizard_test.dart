// ScheduledSnapshotWizardController tests. Covers 3-step happy path
// + retention bounds + per-step error routing.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/scheduled_snapshot/scheduled_snapshot_wizard_controller.dart';
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
  return container.listen(scheduledSnapshotWizardProvider(_key), (_, _) {});
}

void main() {
  group('ScheduledSnapshotWizardController', () {
    test('happy path: nightly retention 7 body', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container
          .read(scheduledSnapshotWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const ScheduledSnapshotForm(
        name: 'nightly-data',
        namespace: 'app',
        sourcePVC: 'data',
        volumeSnapshotClassName: 'csi-hostpath',
        schedule: '0 2 * * *',
        retentionCount: 7,
      ));

      expect(body['name'], 'nightly-data');
      expect(body['sourcePVC'], 'data');
      expect(body['schedule'], '0 2 * * *');
      expect(body['retentionCount'], 7);
    });

    test('retention 0 fails step-1 validation', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(scheduledSnapshotWizardProvider(_key).notifier);
      // Fill step 0 cleanly so we land on step 1.
      notifier.updateForm((f) => f.copyWith(
            name: 'x',
            namespace: 'y',
            sourcePVC: 'data',
            schedule: '0 2 * * *',
          ));
      await notifier.next();
      expect(
          container.read(scheduledSnapshotWizardProvider(_key)).currentStep,
          1);
      // Now step 1 with retention 0 should fail.
      notifier.updateForm((f) => f.copyWith(
            volumeSnapshotClassName: 'csi-hostpath',
            retentionCount: 0,
          ));
      await notifier.next();
      final state = container.read(scheduledSnapshotWizardProvider(_key));
      expect(state.currentStep, 1);
      expect(state.stepErrors[1]?['retentionCount'], isNotNull);
    });

    test('errorRouter splits fields between steps 0 and 1', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c =
          container.read(scheduledSnapshotWizardProvider(_key).notifier);
      expect(c.errorRouter('name'), 0);
      expect(c.errorRouter('namespace'), 0);
      expect(c.errorRouter('sourcePVC'), 0);
      expect(c.errorRouter('schedule'), 0);
      expect(c.errorRouter('volumeSnapshotClassName'), 1);
      expect(c.errorRouter('retentionCount'), 1);
      expect(c.errorRouter('unknown'), null);
    });
  });
}
