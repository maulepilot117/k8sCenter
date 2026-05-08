// Tests for StatefulSetWizardController.

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/statefulset/statefulset_wizard_controller.dart';
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
  return container.listen(statefulSetWizardProvider(_key), (_, _) {});
}

void main() {
  group('StatefulSetWizardController', () {
    test('default form has replicas=1, OrderedReady, no VCTs', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(statefulSetWizardProvider(_key));
      expect(state.form.replicas, 1);
      expect(state.form.podManagementPolicy, 'OrderedReady');
      expect(state.form.volumeClaimTemplates, isEmpty);
    });

    test('validateLocally requires serviceName', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(statefulSetWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web',
            namespace: 'default',
            image: 'nginx',
          ));
      await notifier.next();

      final state = container.read(statefulSetWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['serviceName'], isNotNull);
    });

    test(
        'volumeClaimTemplatesJson drops empty rows and keeps full ones',
        () {
      const form = StatefulSetForm(
        volumeClaimTemplates: [
          VolumeClaimTemplate(
            name: 'data',
            storageClassName: 'standard',
            size: '5Gi',
            accessMode: 'ReadWriteOnce',
          ),
          VolumeClaimTemplate(),
        ],
      );
      final out = form.volumeClaimTemplatesJson();
      expect(out.length, 1);
      expect(out.first['name'], 'data');
      expect(out.first['size'], '5Gi');
    });

    test('happy path with one VCT previews', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/statefulset/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: apps/v1\nkind: StatefulSet\nspec:\n  serviceName: web\n  volumeClaimTemplates:\n  - metadata:\n      name: data\n',
          },
        },
      );

      final notifier =
          container.read(statefulSetWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web',
            namespace: 'default',
            serviceName: 'web',
            replicas: 3,
            image: 'nginx:1.27',
            volumeClaimTemplates: const [
              VolumeClaimTemplate(
                name: 'data',
                storageClassName: 'standard',
                size: '5Gi',
                accessMode: 'ReadWriteOnce',
              ),
            ],
          ));
      await notifier.next();

      final state = container.read(statefulSetWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('volumeClaimTemplates'));
    });

    test('422 with field=serviceName rewinds to Configure', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/statefulset/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': json.encode([
              {'field': 'serviceName', 'message': 'is required'},
            ]),
          },
        },
      );

      final notifier =
          container.read(statefulSetWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web',
            namespace: 'default',
            serviceName: 'X', // pass local; fail server
            image: 'nginx',
          ));
      await notifier.next();

      final state = container.read(statefulSetWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['serviceName'], 'is required');
    });

    test('errorRouter routes volumeClaimTemplates[N].size to step 0', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c =
          container.read(statefulSetWizardProvider(_key).notifier);
      expect(c.errorRouter('volumeClaimTemplates[0].size'), 0);
      expect(c.errorRouter('volumeClaimTemplates[1].name'), 0);
      expect(c.errorRouter('podManagementPolicy'), 0);
    });
  });
}
