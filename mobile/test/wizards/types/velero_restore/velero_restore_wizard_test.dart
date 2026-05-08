// VeleroRestoreWizardController tests.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/velero_restore/velero_restore_wizard_controller.dart';
import 'package:kubecenter/wizards/widgets/key_value_table.dart';
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
  return container.listen(veleroRestoreWizardProvider(_key), (_, _) {});
}

void main() {
  group('VeleroRestoreWizardController', () {
    test('happy path: backup body without mapping', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(veleroRestoreWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const VeleroRestoreForm(
        name: 'production-restore',
        namespace: 'velero',
        backupName: 'production-2026-05-08',
        restorePVs: true,
      ));

      expect(body['name'], 'production-restore');
      expect(body['backupName'], 'production-2026-05-08');
      expect(body['restorePVs'], true);
      expect(body.containsKey('namespaceMapping'), false);
    });

    test('namespace mapping is included when filled', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(veleroRestoreWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const VeleroRestoreForm(
        name: 'r',
        namespace: 'velero',
        backupName: 'b',
        namespaceMapping: [
          KeyValuePair(key: 'production', value: 'production-restored'),
          KeyValuePair(key: '', value: ''), // sentinel — should be dropped
        ],
      ));

      expect(body['namespaceMapping'],
          {'production': 'production-restored'});
    });

    test('missing backup fails validateLocally', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(veleroRestoreWizardProvider(_key).notifier);
      notifier.updateForm(
          (f) => f.copyWith(name: 'r', namespace: 'velero'));
      await notifier.next();

      final state = container.read(veleroRestoreWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['backupName'], isNotNull);
    });
  });
}
