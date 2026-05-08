// Tests for DeploymentWizardController. Covers the 4-step machine,
// errorRouter routing per backend field path, and full preview/apply
// happy path.

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/deployment/deployment_wizard_controller.dart';
import 'package:kubecenter/wizards/widgets/container_form_parts.dart';
import 'package:kubecenter/wizards/widgets/probe_form.dart';
import 'package:kubecenter/wizards/widgets/resources_form.dart';
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
  return container.listen(deploymentWizardProvider(_key), (_, _) {});
}

void main() {
  group('DeploymentWizardController', () {
    test('initial state is formEditing on Basics with default replicas=1',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(deploymentWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.form.replicas, 1);
    });

    test('validateLocally on Basics catches name/namespace/image empty',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(deploymentWizardProvider(_key).notifier);
      await notifier.next();

      final state = container.read(deploymentWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
      expect(state.stepErrors[0]?['image'], isNotNull);
    });

    test('errorRouter routes flat backend paths to the right steps', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(deploymentWizardProvider(_key).notifier);
      expect(c.errorRouter('image'), 0);
      expect(c.errorRouter('replicas'), 0);
      expect(c.errorRouter('envVars[0].name'), 0);
      expect(c.errorRouter('strategy.type'), 0);

      expect(c.errorRouter('ports[0].containerPort'), 1);
      expect(c.errorRouter('ports'), 1);

      expect(c.errorRouter('resources.requestCpu'), 2);
      expect(c.errorRouter('probes.liveness.path'), 2);

      expect(c.errorRouter('totally-unknown'), isNull);
    });

    test(
        'happy path: Basics → Networking → Resources → preview → reviewing',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/deployment/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n',
          },
        },
      );

      final notifier =
          container.read(deploymentWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web',
            namespace: 'default',
            image: 'nginx:1.27',
            replicas: 3,
            ports: const [
              ContainerPortData(
                  name: 'http', containerPort: 80, protocol: 'TCP'),
            ],
            resources: const ResourcesData(
              requestCpu: '100m',
              requestMemory: '128Mi',
              limitCpu: '500m',
              limitMemory: '512Mi',
            ),
            liveness:
                const ProbeData(type: 'http', path: '/healthz', port: 80),
          ));

      await notifier.next(); // Basics → Networking
      expect(container.read(deploymentWizardProvider(_key)).currentStep, 1);

      await notifier.next(); // Networking → Resources
      expect(container.read(deploymentWizardProvider(_key)).currentStep, 2);

      await notifier.next(); // Resources → preview → reviewing
      final state = container.read(deploymentWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.currentStep, 3);
      expect(state.previewYaml, contains('Deployment'));
    });

    test('422 with field=image rewinds to Basics step', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/deployment/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': json.encode([
              {'field': 'image', 'message': 'is required'},
            ]),
          },
        },
      );

      final notifier =
          container.read(deploymentWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web',
            namespace: 'default',
            image: 'X', // pass local validation, fail server
          ));

      // Advance through steps to trigger preview at step 2.
      await notifier.next(); // 0 → 1
      await notifier.next(); // 1 → 2
      await notifier.next(); // preview, 422 routes back to step 0

      final state = container.read(deploymentWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['image'], 'is required');
    });

    test('toPreviewBody omits empty optional sections', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(deploymentWizardProvider(_key).notifier);
      const form = DeploymentForm(
        name: 'web',
        namespace: 'default',
        image: 'nginx',
        replicas: 1,
      );
      final body = c.toPreviewBody(form);
      expect(body['name'], 'web');
      expect(body['image'], 'nginx');
      expect(body.containsKey('ports'), false);
      expect(body.containsKey('envVars'), false);
      expect(body.containsKey('resources'), false);
      expect(body.containsKey('probes'), false);
      expect(body.containsKey('labels'), false);
    });
  });
}
