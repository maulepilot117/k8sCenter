// Tests for SecretWizardController. Mirrors the ConfigMap suite but
// exercises the type dropdown and the type-specific validation paths.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/secret/secret_wizard_controller.dart';
import 'package:kubecenter/wizards/widgets/key_value_table.dart';
import 'package:kubecenter/wizards/wizard_controller.dart';

import '../support/mock_dio_adapter.dart';

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
  return container.listen(secretWizardProvider(_key), (_, _) {});
}

void main() {
  group('SecretWizardController', () {
    test('default form has type=Opaque and empty data', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(secretWizardProvider(_key));
      expect(state.form.type, 'Opaque');
      expect(state.form.data, isEmpty);
      expect(state.currentStep, 0);
    });

    test('next() with empty form populates step errors', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(secretWizardProvider(_key).notifier);
      await notifier.next();

      final state = container.read(secretWizardProvider(_key));
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
      expect(state.stepErrors[0]?['data'], isNotNull);
      expect(state.currentStep, 0);
    });

    test('valid TLS form with raw values posts {data, type, ...} and previews',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/secret/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: v1\nkind: Secret\ntype: kubernetes.io/tls\n',
          },
        },
      );

      final notifier = container.read(secretWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web-tls',
            namespace: 'default',
            type: 'kubernetes.io/tls',
            data: const [
              KeyValuePair(key: 'tls.crt', value: '...PEM...'),
              KeyValuePair(key: 'tls.key', value: '...KEY...'),
            ],
          ));
      await notifier.next();

      final state = container.read(secretWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('Secret'));

      // Confirm the request body shape — type and data passed through.
      final req = mock.requests
          .firstWhere((r) => r.path == '/api/v1/wizards/secret/preview');
      expect(req.data, isA<Map<String, dynamic>>());
      final body = req.data as Map<String, dynamic>;
      expect(body['type'], 'kubernetes.io/tls');
      expect((body['data'] as Map<String, dynamic>)['tls.crt'], '...PEM...');
    });

    test('errorRouter recognizes data.<key> paths from secret.go validation',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final ctl = container.read(secretWizardProvider(_key).notifier);
      // data.tls.crt is the TLS-required-field path the backend uses.
      expect(ctl.errorRouter('data.tls.crt'), 0);
      expect(ctl.errorRouter('data..dockerconfigjson'), 0);
      expect(ctl.errorRouter('type'), 0);
      expect(ctl.errorRouter('unknown.path'), isNull);
    });
  });
}
