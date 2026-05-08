// PvcWizardController tests. Covers wire body shape, optional
// dataSource (RestoreSnapshot path), and validateLocally errors.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/pvc/pvc_wizard_controller.dart';
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
  return container.listen(pvcWizardProvider(_key), (_, _) {});
}

void main() {
  group('PvcWizardController', () {
    test('happy path: 5Gi RWO PVC body', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(pvcWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const PvcForm(
        name: 'data',
        namespace: 'app',
        storageClassName: 'standard',
        sizeValue: '5',
        sizeUnit: 'Gi',
        accessMode: 'ReadWriteOnce',
      ));

      expect(body['name'], 'data');
      expect(body['namespace'], 'app');
      expect(body['storageClassName'], 'standard');
      expect(body['size'], '5Gi');
      expect(body['accessMode'], 'ReadWriteOnce');
      expect(body.containsKey('dataSource'), false);
    });

    test('dataSource is included when set (RestoreSnapshot path)', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(pvcWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const PvcForm(
        name: 'data-restored',
        namespace: 'app',
        storageClassName: 'standard',
        sizeValue: '10',
        sizeUnit: 'Gi',
        accessMode: 'ReadWriteOnce',
        dataSource: PvcDataSource(name: 'snap-2026-05-08'),
      ));

      final ds = body['dataSource'] as Map<String, dynamic>;
      expect(ds['name'], 'snap-2026-05-08');
      expect(ds['kind'], 'VolumeSnapshot');
      expect(ds['apiGroup'], 'snapshot.storage.k8s.io');
    });

    test('blank name + namespace + storageClass surface as step-0 errors',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(pvcWizardProvider(_key).notifier);
      await notifier.next();

      final state = container.read(pvcWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
      expect(state.stepErrors[0]?['storageClassName'], isNotNull);
    });

    test('non-positive size flagged as size error', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(pvcWizardProvider(_key).notifier);
      final errs = c.validateLocally(
          const PvcForm(
            name: 'd',
            namespace: 'a',
            storageClassName: 's',
            sizeValue: '0',
            accessMode: 'ReadWriteOnce',
          ),
          0);
      expect(errs['size'], isNotNull);
    });

    test('errorRouter routes dataSource.* paths to step 0', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(pvcWizardProvider(_key).notifier);
      expect(c.errorRouter('dataSource.name'), 0);
      expect(c.errorRouter('dataSource.kind'), 0);
      expect(c.errorRouter('storageClassName'), 0);
      expect(c.errorRouter('size'), 0);
      expect(c.errorRouter('unknownPath'), null);
    });
  });
}
