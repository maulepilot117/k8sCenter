// VeleroBackupWizardController tests. Covers wire body shape,
// included/excluded mutual-exclusion check, and TTL validation.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/velero_backup/velero_backup_wizard_controller.dart';
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
  return container.listen(veleroBackupWizardProvider(_key), (_, _) {});
}

void main() {
  group('VeleroBackupWizardController', () {
    test('happy path: production namespace 168h TTL body', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(veleroBackupWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const VeleroBackupForm(
        name: 'production-backup',
        namespace: 'velero',
        includedNamespaces: {'production'},
        ttl: '168h',
        snapshotVolumes: true,
      ));

      expect(body['name'], 'production-backup');
      expect(body['namespace'], 'velero');
      expect(body['includedNamespaces'], ['production']);
      expect(body['ttl'], '168h');
      expect(body['snapshotVolumes'], true);
      expect(body.containsKey('excludedNamespaces'), false);
      expect(body.containsKey('storageLocation'), false);
    });

    test('overlap between included and excluded fails validateLocally',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(veleroBackupWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'b',
            namespace: 'velero',
            includedNamespaces: {'app', 'production'},
            excludedNamespaces: {'app'},
          ));
      await notifier.next();

      final state = container.read(veleroBackupWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['includedNamespaces'], isNotNull);
    });

    test('invalid TTL fails validateLocally', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(veleroBackupWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'b',
            namespace: 'velero',
            ttl: 'bogus',
          ));
      await notifier.next();

      final state = container.read(veleroBackupWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['ttl'], isNotNull);
    });

    test('empty TTL is allowed (Velero default applies)', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(veleroBackupWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const VeleroBackupForm(
        name: 'b',
        namespace: 'velero',
      ));
      expect(body.containsKey('ttl'), false);
    });
  });
}
