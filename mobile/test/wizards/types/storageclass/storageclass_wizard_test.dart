// Tests for StorageClassWizardController. Cluster-scoped — no
// namespace field. Covers happy path with provisioner + params, the
// optional fields' suppression, and validation.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/storageclass/storageclass_wizard_controller.dart';
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
  return container.listen(storageClassWizardProvider(_key), (_, _) {});
}

void main() {
  group('StorageClassWizardController', () {
    test('happy path: aws-ebs gp3 retain with parameters', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(storageClassWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const StorageClassForm(
        name: 'fast-ssd',
        provisioner: 'kubernetes.io/aws-ebs',
        reclaimPolicy: 'Retain',
        volumeBindingMode: 'Immediate',
        allowVolumeExpansion: true,
        parameters: [KeyValuePair(key: 'type', value: 'gp3')],
        mountOptions: 'noatime\nnodiratime',
      ));

      expect(body['name'], 'fast-ssd');
      expect(body['provisioner'], 'kubernetes.io/aws-ebs');
      expect(body['reclaimPolicy'], 'Retain');
      expect(body['allowVolumeExpansion'], true);
      expect(body['parameters'], {'type': 'gp3'});
      expect(body['mountOptions'], ['noatime', 'nodiratime']);
      expect(body.containsKey('isDefault'), false);
      expect(body.containsKey('namespace'), false,
          reason: 'StorageClass is cluster-scoped');
    });

    test('blank provisioner fails validateLocally', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(storageClassWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'x'));
      await notifier.next();

      final state = container.read(storageClassWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['provisioner'], isNotNull);
    });

    test('isDefault toggle propagates to body', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(storageClassWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const StorageClassForm(
        name: 'default-sc',
        provisioner: 'csi.driver',
        isDefault: true,
      ));
      expect(body['isDefault'], true);
    });
  });
}
