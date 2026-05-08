// Tests for HpaWizardController. Covers happy path body shape,
// min > max validation, and errorRouter routing.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/hpa/hpa_wizard_controller.dart';
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
  return container.listen(hpaWizardProvider(_key), (_, _) {});
}

void main() {
  group('HpaWizardController', () {
    test('happy path: Deployment/web min=2 max=10 CPU 80%', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(hpaWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const HpaForm(
        name: 'web-hpa',
        namespace: 'default',
        targetKind: 'Deployment',
        targetName: 'web',
        minReplicas: 2,
        maxReplicas: 10,
        metrics: [HpaMetric(resourceName: 'cpu', targetAverageValue: 80)],
      ));

      expect(body['targetKind'], 'Deployment');
      expect(body['targetName'], 'web');
      expect(body['minReplicas'], 2);
      expect(body['maxReplicas'], 10);
      final m = (body['metrics'] as List).first as Map<String, dynamic>;
      expect(m['type'], 'Resource');
      expect(m['resourceName'], 'cpu');
      expect(m['targetType'], 'Utilization');
      expect(m['targetAverageValue'], 80);
    });

    test('min > max: validateLocally surfaces inline error', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(hpaWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'bad',
            namespace: 'default',
            targetKind: 'Deployment',
            targetName: 'web',
            minReplicas: 20,
            maxReplicas: 5,
          ));
      await notifier.next();

      final state = container.read(hpaWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['minReplicas'], contains('maxReplicas'));
    });

    test('omits minReplicas when null', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(hpaWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const HpaForm(
        name: 'noop',
        namespace: 'default',
        targetKind: 'Deployment',
        targetName: 'web',
        maxReplicas: 5,
      ));
      expect(body.containsKey('minReplicas'), false);
    });

    test('errorRouter routes metrics paths to step 0', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(hpaWizardProvider(_key).notifier);
      expect(c.errorRouter('targetName'), 0);
      expect(c.errorRouter('metrics[0].targetAverageValue'), 0);
      expect(c.errorRouter('totally-unknown'), isNull);
    });
  });
}
