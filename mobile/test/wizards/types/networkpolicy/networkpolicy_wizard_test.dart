// Tests for NetworkPolicyWizardController. Covers the quarantine
// pattern (no rules), an allow-from-namespace policy, podSelector
// validation, and the errorRouter routing.

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/networkpolicy/networkpolicy_wizard_controller.dart';
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
  return container.listen(networkPolicyWizardProvider(_key), (_, _) {});
}

void main() {
  group('NetworkPolicyWizardController', () {
    test('quarantine: policyTypes Ingress+Egress, no rules → empty arrays',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(networkPolicyWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const NetworkPolicyForm(
        name: 'quarantine',
        namespace: 'tenant-a',
        includeIngress: true,
        includeEgress: true,
      ));

      expect(body['name'], 'quarantine');
      expect(body['policyTypes'], ['Ingress', 'Egress']);
      expect(body['ingress'], <Map<String, dynamic>>[]);
      expect(body['egress'], <Map<String, dynamic>>[]);
      expect(body['podSelector'], <String, String>{});
    });

    test('allow-from-namespace policy emits namespaceSelector peer', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(networkPolicyWizardProvider(_key).notifier);
      final form = NetworkPolicyForm(
        name: 'allow-monitoring',
        namespace: 'app',
        podSelector: const [KeyValuePair(key: 'app', value: 'web')],
        includeIngress: true,
        ingress: [
          NetworkPolicyRule(
            peers: const [
              NetworkPolicyPeer(
                kind: PeerKind.namespaceSel,
                namespaceSelector: [KeyValuePair(key: 'team', value: 'monitoring')],
              ),
            ],
            ports: const [NetworkPolicyPort(port: 9090, protocol: 'TCP')],
          ),
        ],
      );
      final body = c.toPreviewBody(form);

      expect(body['policyTypes'], ['Ingress']);
      final ingress = body['ingress'] as List;
      expect(ingress.length, 1);
      final rule = ingress.first as Map<String, dynamic>;
      expect(rule['from'], [
        {
          'namespaceSelector': {'team': 'monitoring'},
        },
      ]);
      expect(rule['ports'], [
        {'port': 9090, 'protocol': 'TCP'},
      ]);
    });

    test('podSelector validation: missing name reports inline', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(networkPolicyWizardProvider(_key).notifier);
      // Disable both directions to also exercise policyTypes error.
      notifier.updateForm((f) => f.copyWith(
            includeIngress: false,
            includeEgress: false,
          ));
      await notifier.next();

      final state = container.read(networkPolicyWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
      expect(state.stepErrors[0]?['policyTypes'], isNotNull);
    });

    test('peer with empty CIDR caught by validateLocally (no silent drop)',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(networkPolicyWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'np',
            namespace: 'default',
            includeIngress: true,
            ingress: [
              const NetworkPolicyRule(peers: [
                NetworkPolicyPeer(kind: PeerKind.ipBlock),
              ]),
            ],
          ));
      await notifier.next();

      final state = container.read(networkPolicyWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(
        state.stepErrors[0]?['ingress[0].from[0].ipBlock.cidr'],
        isNotNull,
      );
    });

    test('422 with ingress[2] error rewinds to Configure with the message',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/networkpolicy/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': json.encode([
              {
                'field': 'ingress[2].ports[0].port',
                'message': 'must be between 1 and 65535',
              },
            ]),
          },
        },
      );

      final notifier =
          container.read(networkPolicyWizardProvider(_key).notifier);
      // Build a form with three rules; backend will complain about
      // index 2. Pass-through validateLocally by giving each rule a
      // valid pod-selector peer + a bad port (port=0 sneaks past local
      // gates).
      notifier.updateForm((f) => f.copyWith(
            name: 'np',
            namespace: 'default',
            includeIngress: true,
            ingress: [
              for (var i = 0; i < 3; i++)
                NetworkPolicyRule(
                  peers: const [
                    NetworkPolicyPeer(
                      kind: PeerKind.pod,
                      podSelector: [KeyValuePair(key: 'app', value: 'web')],
                    ),
                  ],
                  ports: const [NetworkPolicyPort(port: 80)],
                ),
            ],
          ));
      await notifier.next();

      final state = container.read(networkPolicyWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(
        state.stepErrors[0]?['ingress[2].ports[0].port'],
        contains('between 1 and 65535'),
      );
    });

    test('errorRouter routes ingress/egress/policyTypes paths to step 0',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(networkPolicyWizardProvider(_key).notifier);
      expect(c.errorRouter('name'), 0);
      expect(c.errorRouter('podSelector'), 0);
      expect(c.errorRouter('policyTypes[0]'), 0);
      expect(c.errorRouter('ingress[0].from[0].ipBlock.cidr'), 0);
      expect(c.errorRouter('egress[0].ports[0].port'), 0);
      expect(c.errorRouter('totally-unknown'), isNull);
    });
  });
}
