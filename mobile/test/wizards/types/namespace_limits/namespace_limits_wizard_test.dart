// Tests for NamespaceLimitsWizardController. Multi-resource:
// preview YAML is two `---`-separated docs; apply summary handles
// per-doc results. Covers happy path body shape, multi-doc preview
// from a mocked backend response, and required-field validation.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/namespace_limits/namespace_limits_wizard_controller.dart';
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
  return container.listen(namespaceLimitsWizardProvider(_key), (_, _) {});
}

NamespaceLimitsForm _validForm() => const NamespaceLimitsForm(
      namespace: 'tenant-a',
      quotaName: 'tenant-a-quota',
      limitRangeName: 'tenant-a-limits',
      cpuHard: '4',
      memoryHard: '8Gi',
      podsHard: 100,
      containerDefault: ResourcePair(cpu: '200m', memory: '256Mi'),
      containerDefaultRequest: ResourcePair(cpu: '100m', memory: '128Mi'),
      containerMax: ResourcePair(cpu: '500m', memory: '512Mi'),
      containerMin: ResourcePair(cpu: '50m', memory: '64Mi'),
    );

void main() {
  group('NamespaceLimitsWizardController', () {
    test('happy path body shape: namespace + quota + limits', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c =
          container.read(namespaceLimitsWizardProvider(_key).notifier);
      final body = c.toPreviewBody(_validForm());
      expect(body['namespace'], 'tenant-a');
      expect(body['quotaName'], 'tenant-a-quota');
      expect(body['limitRangeName'], 'tenant-a-limits');
      expect((body['quota'] as Map)['cpuHard'], '4');
      expect((body['quota'] as Map)['memoryHard'], '8Gi');
      expect((body['quota'] as Map)['podsHard'], 100);
      final limits = body['limits'] as Map;
      expect(limits['containerDefault'],
          {'cpu': '200m', 'memory': '256Mi'});
      expect(limits['containerMin'], {'cpu': '50m', 'memory': '64Mi'});
    });

    test('multi-doc preview YAML survives the round-trip', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      const multiDoc =
          'apiVersion: v1\nkind: ResourceQuota\nmetadata:\n  name: q\n---\n'
          'apiVersion: v1\nkind: LimitRange\nmetadata:\n  name: lr\n';
      mock.onJson(
        'POST',
        '/api/v1/wizards/namespace-limits/preview',
        body: {
          'data': {'yaml': multiDoc},
        },
      );

      final notifier =
          container.read(namespaceLimitsWizardProvider(_key).notifier);
      notifier.updateForm((_) => _validForm());
      await notifier.next();

      final state = container.read(namespaceLimitsWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('ResourceQuota'));
      expect(state.previewYaml, contains('---'));
      expect(state.previewYaml, contains('LimitRange'));
    });

    test('missing required fields routed inline', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(namespaceLimitsWizardProvider(_key).notifier);
      await notifier.next();

      final state = container.read(namespaceLimitsWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
      expect(state.stepErrors[0]?['quotaName'], isNotNull);
      expect(state.stepErrors[0]?['limitRangeName'], isNotNull);
      expect(state.stepErrors[0]?['quota.cpuHard'], isNotNull);
      expect(state.stepErrors[0]?['limits.containerDefault.cpu'], isNotNull);
    });

    test('errorRouter routes nested quota.* and limits.* paths', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c =
          container.read(namespaceLimitsWizardProvider(_key).notifier);
      expect(c.errorRouter('quota.cpuHard'), 0);
      expect(c.errorRouter('limits.containerMax.memory'), 0);
      expect(c.errorRouter('quotaName'), 0);
      expect(c.errorRouter('totally-unknown'), isNull);
    });
  });
}
