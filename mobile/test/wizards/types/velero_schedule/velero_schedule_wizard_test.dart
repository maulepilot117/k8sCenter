// VeleroScheduleWizardController tests.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/velero_schedule/velero_schedule_wizard_controller.dart';
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
  return container.listen(veleroScheduleWizardProvider(_key), (_, _) {});
}

void main() {
  group('VeleroScheduleWizardController', () {
    test('happy path: nightly schedule with production template body', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(veleroScheduleWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const VeleroScheduleForm(
        name: 'production-nightly',
        namespace: 'velero',
        schedule: '0 1 * * *',
        includedNamespaces: {'production'},
        ttl: '168h',
        snapshotVolumes: true,
      ));

      expect(body['name'], 'production-nightly');
      expect(body['schedule'], '0 1 * * *');
      expect(body['includedNamespaces'], ['production']);
      expect(body['ttl'], '168h');
      expect(body.containsKey('paused'), false);
    });

    test('paused emits explicit true', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(veleroScheduleWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const VeleroScheduleForm(
        name: 'pn',
        namespace: 'velero',
        schedule: '@hourly',
        paused: true,
      ));
      expect(body['paused'], true);
    });

    test('errorRouter splits step 0 and step 1 fields', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(veleroScheduleWizardProvider(_key).notifier);
      expect(c.errorRouter('schedule'), 0);
      expect(c.errorRouter('paused'), 0);
      expect(c.errorRouter('ttl'), 1);
      expect(c.errorRouter('includedNamespaces'), 1);
      expect(c.errorRouter('snapshotVolumes'), 1);
      expect(c.errorRouter('unknown'), null);
    });

    test('overlapping included/excluded fails step-1 validation', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(veleroScheduleWizardProvider(_key).notifier);
      // Step 0 OK
      notifier.updateForm((f) => f.copyWith(
            name: 's',
            namespace: 'velero',
            schedule: '0 1 * * *',
          ));
      await notifier.next();
      expect(
          container.read(veleroScheduleWizardProvider(_key)).currentStep, 1);
      // Step 1 conflict
      notifier.updateForm((f) => f.copyWith(
            includedNamespaces: {'app'},
            excludedNamespaces: {'app'},
          ));
      await notifier.next();
      final state = container.read(veleroScheduleWizardProvider(_key));
      expect(state.currentStep, 1);
      expect(state.stepErrors[1]?['includedNamespaces'], isNotNull);
    });
  });
}
