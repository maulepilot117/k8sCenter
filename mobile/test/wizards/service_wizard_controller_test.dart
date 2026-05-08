// Tests for ServiceWizardController. Heavier than ConfigMap/Secret
// because the form has nested ports + selector/labels maps and more
// branches in validateLocally.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/service/service_wizard_controller.dart';
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
  return container.listen(serviceWizardProvider(_key), (_, _) {});
}

void main() {
  group('ServiceWizardController', () {
    test('default form has type=ClusterIP and one empty port row', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(serviceWizardProvider(_key));
      expect(state.form.type, 'ClusterIP');
      expect(state.form.ports.length, 1);
      expect(state.form.ports.first.isEmpty, true);
    });

    test(
        'validateLocally rejects empty selector and zero ports with '
        'distinct messages', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(serviceWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web',
            namespace: 'default',
          ));
      await notifier.next();

      final state = container.read(serviceWizardProvider(_key));
      expect(state.stepErrors[0]?['selector'], isNotNull);
      expect(state.stepErrors[0]?['ports'], isNotNull);
      expect(state.currentStep, 0);
    });

    test(
        'portsAsJson strips the trailing empty sentinel and includes '
        'protocol when set', () {
      const form = ServiceForm(
        ports: [
          ServicePort(port: 80, targetPort: 8080, protocol: 'TCP'),
          ServicePort(port: 443, targetPort: 8443, protocol: 'TCP'),
          ServicePort(), // empty sentinel
        ],
      );
      final json = form.portsAsJson();
      expect(json.length, 2);
      expect(json[0]['port'], 80);
      expect(json[0]['targetPort'], 8080);
      expect(json[0]['protocol'], 'TCP');
      expect(json[1]['port'], 443);
    });

    test('selectorAsMap and labelsAsMap drop empty-key rows', () {
      const form = ServiceForm(
        selector: [
          KeyValuePair(key: 'app', value: 'web'),
          KeyValuePair(key: '', value: 'leftover'),
        ],
        labels: [
          KeyValuePair(key: 'tier', value: 'frontend'),
        ],
      );
      expect(form.selectorAsMap(), {'app': 'web'});
      expect(form.labelsAsMap(), {'tier': 'frontend'});
    });

    test('valid form previews and surfaces YAML', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/service/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: v1\nkind: Service\nspec:\n  type: ClusterIP\n',
          },
        },
      );

      final notifier = container.read(serviceWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web',
            namespace: 'default',
            type: 'ClusterIP',
            selector: const [KeyValuePair(key: 'app', value: 'web')],
            ports: const [
              ServicePort(port: 80, targetPort: 8080, protocol: 'TCP'),
            ],
          ));
      await notifier.next();

      final state = container.read(serviceWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('Service'));
    });

    test('errorRouter recognizes ports[N].port and selector', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final ctl = container.read(serviceWizardProvider(_key).notifier);
      expect(ctl.errorRouter('ports[0].port'), 0);
      expect(ctl.errorRouter('ports[0].targetPort'), 0);
      expect(ctl.errorRouter('selector'), 0);
      expect(ctl.errorRouter('labels'), 0);
      expect(ctl.errorRouter('totally-unknown'), isNull);
    });
  });
}
