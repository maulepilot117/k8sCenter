// Tests for IngressWizardController. Covers happy-path body shape,
// missing-service-name local validation, and TLS section emission.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/ingress/ingress_wizard_controller.dart';
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
  return container.listen(ingressWizardProvider(_key), (_, _) {});
}

void main() {
  group('IngressWizardController', () {
    test('happy path body shape: one rule, two paths', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(ingressWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const IngressForm(
        name: 'web',
        namespace: 'default',
        rules: [
          IngressRule(host: 'app.example.com', paths: [
            IngressPath(
              path: '/',
              pathType: 'Prefix',
              serviceName: 'web',
              servicePort: 80,
            ),
            IngressPath(
              path: '/api',
              pathType: 'Prefix',
              serviceName: 'api',
              servicePort: 8080,
            ),
          ]),
        ],
      ));

      expect(body['name'], 'web');
      expect((body['rules'] as List).length, 1);
      final rule = (body['rules'] as List).first as Map<String, dynamic>;
      expect(rule['host'], 'app.example.com');
      expect((rule['paths'] as List).length, 2);
      expect((rule['paths'] as List).first['servicePort'], 80);
      expect(body.containsKey('tls'), false);
      expect(body.containsKey('ingressClassName'), false);
    });

    test('missing service-name flagged inline by validateLocally',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(ingressWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web',
            namespace: 'default',
            rules: const [
              IngressRule(paths: [
                IngressPath(servicePort: 80),
              ]),
            ],
          ));
      await notifier.next();

      final state = container.read(ingressWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['rules[0].paths[0].serviceName'], isNotNull);
    });

    test('TLS section emits secretName + hosts when present', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(ingressWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const IngressForm(
        name: 'web',
        namespace: 'default',
        ingressClassName: 'nginx',
        rules: [
          IngressRule(paths: [
            IngressPath(
                path: '/', pathType: 'Prefix', serviceName: 'web', servicePort: 443),
          ]),
        ],
        tls: [
          IngressTls(hosts: ['app.example.com'], secretName: 'web-tls'),
        ],
      ));

      expect(body['ingressClassName'], 'nginx');
      expect((body['tls'] as List).length, 1);
      final t = (body['tls'] as List).first as Map<String, dynamic>;
      expect(t['hosts'], ['app.example.com']);
      expect(t['secretName'], 'web-tls');
    });

    test('errorRouter routes rules/tls paths to step 0', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(ingressWizardProvider(_key).notifier);
      expect(c.errorRouter('rules[0].paths[0].serviceName'), 0);
      expect(c.errorRouter('tls[0].secretName'), 0);
      expect(c.errorRouter('ingressClassName'), 0);
      expect(c.errorRouter('totally-unknown'), isNull);
    });
  });
}
