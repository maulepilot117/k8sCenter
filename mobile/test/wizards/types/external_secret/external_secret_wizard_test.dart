// Tests for ExternalSecretWizardController via the generic
// WizardController state machine. Mirrors certificate_wizard_test.dart's
// shape so reviewers can audit one wizard's tests as a template for
// every other.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/external_secret/external_secret_wizard_controller.dart';
import 'package:kubecenter/wizards/widgets/store_picker.dart';
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
  return container.listen(externalSecretWizardProvider(_key), (_, _) {});
}

void main() {
  group('ExternalSecretWizardController', () {
    test('initial state: formEditing on step 0; one empty data row', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(externalSecretWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.form.data.length, 1);
      expect(state.form.refreshInterval, '1h');
    });

    test('validateLocally blocks advance with empty form', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(externalSecretWizardProvider(_key).notifier);
      await notifier.next();
      final state = container.read(externalSecretWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
      expect(state.stepErrors[0]?['storeRef.name'], isNotNull);
      expect(state.stepErrors[0]?['targetSecretName'], isNotNull);
      expect(state.stepErrors[0]?['data'], isNotNull);
    });

    test('happy path: valid form runs preview and transitions to reviewing',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/external-secret/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: external-secrets.io/v1\nkind: ExternalSecret\n'
          },
        },
      );

      final notifier =
          container.read(externalSecretWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'db-creds',
            namespace: 'app',
            storeRef: const StoreSelection(
                name: 'vault-shared', kind: 'ClusterSecretStore'),
            targetSecretName: 'db-creds',
            data: const [
              EsoDataItem(secretKey: 'password', remoteKey: 'kv/db'),
            ],
          ));
      await notifier.next();

      final state = container.read(externalSecretWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.currentStep, 1);
      expect(state.previewYaml, contains('ExternalSecret'));
    });

    test('toPreviewBody: drops fully-empty data rows; emits property when set',
        () {
      final form = ExternalSecretForm(
        name: 'db-creds',
        namespace: 'app',
        storeRef: const StoreSelection(
            name: 'vault-shared', kind: 'ClusterSecretStore'),
        targetSecretName: 'db-creds',
        data: const [
          EsoDataItem(secretKey: 'password', remoteKey: 'kv/db'),
          EsoDataItem(),
          EsoDataItem(
            secretKey: 'username',
            remoteKey: 'kv/db',
            remoteProperty: 'user',
          ),
        ],
      );
      final body = ExternalSecretWizardController().toPreviewBody(form);
      final items = body['data'] as List;
      expect(items.length, 2);
      expect((items[0] as Map)['secretKey'], 'password');
      expect((items[1] as Map)['remoteRef'],
          {'key': 'kv/db', 'property': 'user'});
      // refreshInterval default '1h' is emitted.
      expect(body['refreshInterval'], '1h');
      expect(body['storeRef'], {
        'name': 'vault-shared',
        'kind': 'ClusterSecretStore',
      });
    });

    test('422 with data[0].secretKey path routes to Configure', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/external-secret/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail':
                '[{"field":"data[0].secretKey","message":"duplicate secretKey"}]'
          }
        },
      );

      final notifier =
          container.read(externalSecretWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'db-creds',
            namespace: 'app',
            storeRef:
                const StoreSelection(name: 'vault-shared', kind: 'SecretStore'),
            targetSecretName: 'db-creds',
            data: const [
              EsoDataItem(secretKey: 'password', remoteKey: 'kv/db'),
            ],
          ));
      await notifier.next();

      final state = container.read(externalSecretWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['data[0].secretKey'], contains('duplicate'));
    });
  });
}
