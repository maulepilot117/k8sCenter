// Tests for JobWizardController.

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/job/job_wizard_controller.dart';
import 'package:kubecenter/wizards/widgets/container_form_parts.dart';
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
  return container.listen(jobWizardProvider(_key), (_, _) {});
}

void main() {
  group('JobWizardController', () {
    test('default form has restartPolicy=Never', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(jobWizardProvider(_key));
      expect(state.form.restartPolicy, 'Never');
    });

    test('toPreviewBody nests image under container, omits null int fields',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(jobWizardProvider(_key).notifier);
      const form = JobForm(
        name: 'j',
        namespace: 'default',
        image: 'busybox',
        restartPolicy: 'OnFailure',
        envVars: [EnvVarData(name: 'A', value: '1')],
      );
      final body = c.toPreviewBody(form);
      expect(body['name'], 'j');
      expect(body['restartPolicy'], 'OnFailure');
      expect((body['container'] as Map)['image'], 'busybox');
      expect((body['container'] as Map)['envVars'], [
        {'name': 'A', 'value': '1'},
      ]);
      expect(body.containsKey('parallelism'), false);
      expect(body.containsKey('completions'), false);
      expect(body.containsKey('backoffLimit'), false);
    });

    test('toPreviewBody includes int fields when set', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(jobWizardProvider(_key).notifier);
      const form = JobForm(
        name: 'j',
        namespace: 'default',
        image: 'busybox',
        parallelism: 3,
        completions: 6,
        backoffLimit: 4,
      );
      final body = c.toPreviewBody(form);
      expect(body['parallelism'], 3);
      expect(body['completions'], 6);
      expect(body['backoffLimit'], 4);
    });

    test('errorRouter routes container.* to step 0', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(jobWizardProvider(_key).notifier);
      expect(c.errorRouter('container.image'), 0);
      expect(c.errorRouter('container.envVars[0].name'), 0);
      expect(c.errorRouter('parallelism'), 0);
      expect(c.errorRouter('totally-unknown'), isNull);
    });

    test('happy path previews and surfaces YAML', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/job/preview',
        body: {
          'data': {'yaml': 'apiVersion: batch/v1\nkind: Job\n'},
        },
      );

      final notifier = container.read(jobWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'j',
            namespace: 'default',
            image: 'busybox:latest',
            restartPolicy: 'OnFailure',
            parallelism: 2,
          ));
      await notifier.next();

      final state = container.read(jobWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('Job'));
    });

    test('422 with field=container.image rewinds to Configure', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/job/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': json.encode([
              {'field': 'container.image', 'message': 'is required'},
            ]),
          },
        },
      );

      final notifier = container.read(jobWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'j',
            namespace: 'default',
            image: 'X',
          ));
      await notifier.next();

      final state = container.read(jobWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['container.image'], 'is required');
    });
  });
}
