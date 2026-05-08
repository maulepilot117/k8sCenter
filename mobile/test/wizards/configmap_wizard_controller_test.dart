// Tests for ConfigMapWizardController via the generic WizardController
// state machine.
//
// Covers:
//   - Initial state: formEditing on step 0 with empty form
//   - validateLocally blocks advance when name/namespace/data empty
//   - next() from Configure when valid runs preview, transitions to
//     reviewing with previewYaml populated
//   - 422 from preview routes errors back to Configure step (step 0)
//     and populates state.stepErrors
//   - apply() success transitions to applied + populates outcome
//   - Cluster mismatch on apply aborts via failed status

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_provider.dart';
import 'package:kubecenter/wizards/types/configmap/configmap_wizard_controller.dart';
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
  return container.listen(
    configMapWizardProvider(_key),
    (_, _) {},
  );
}

void main() {
  group('ConfigMapWizardController', () {
    test('initial state is formEditing on step 0 with empty form', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.form.name, '');
      expect(state.form.namespace, '');
      expect(state.form.data, isEmpty);
      expect(state.previewYaml, isNull);
    });

    test('next() with empty form populates step errors and stays put',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(configMapWizardProvider(_key).notifier);
      await notifier.next();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
      expect(state.stepErrors[0]?['data'], isNotNull);
    });

    test(
        'next() with valid form runs preview, transitions to reviewing '
        'with YAML populated', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        body: {
          'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
        },
      );

      final notifier =
          container.read(configMapWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'cfg',
            namespace: 'default',
            data: const [KeyValuePair(key: 'k', value: 'v')],
          ));
      await notifier.next();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.currentStep, 1);
      expect(state.previewYaml, contains('ConfigMap'));
    });

    test('422 routes errors back to Configure step', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': json.encode([
              {'field': 'name', 'message': 'must be a valid DNS label'},
            ]),
          },
        },
      );

      final notifier =
          container.read(configMapWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'BAD',
            namespace: 'default',
            data: const [KeyValuePair(key: 'k', value: 'v')],
          ));
      await notifier.next();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], contains('DNS label'));
    });

    test('apply() success transitions to applied + populates outcome',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        body: {
          'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
        },
      );
      mock.onJson(
        'POST',
        '/api/v1/yaml/apply',
        body: {
          'data': {
            'results': [
              {
                'index': 0,
                'kind': 'ConfigMap',
                'name': 'cfg',
                'namespace': 'default',
                'action': 'created',
              },
            ],
            'summary': {
              'total': 1,
              'created': 1,
              'configured': 0,
              'unchanged': 0,
              'failed': 0,
            },
          },
        },
      );

      final notifier =
          container.read(configMapWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'cfg',
            namespace: 'default',
            data: const [KeyValuePair(key: 'k', value: 'v')],
          ));
      await notifier.next();
      await notifier.apply();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.applied);
      expect(state.applyOutcome, isNotNull);
      expect(state.applyOutcome!.created, 1);
      expect(state.applyOutcome!.firstResultName, 'cfg');
      expect(state.applyOutcome!.firstResultNamespace, 'default');
    });

    test(
        'apply() with cluster drift surfaces failed status without firing '
        'the request', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        body: {
          'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
        },
      );

      final notifier =
          container.read(configMapWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'cfg',
            namespace: 'default',
            data: const [KeyValuePair(key: 'k', value: 'v')],
          ));
      await notifier.next();

      // Operator switches clusters mid-wizard. The pin should now mismatch.
      container.read(activeClusterProvider.notifier).setCluster('other');
      await notifier.apply();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.failed);
      expect(state.errorMessage, contains('Cluster changed'));

      // Verify no apply request reached the mock.
      final applyRequests = mock.requests
          .where((r) => r.path == '/api/v1/yaml/apply')
          .toList();
      expect(applyRequests, isEmpty);
    });

    test('back() from Review returns to Configure clearing previewYaml',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/configmap/preview',
        body: {
          'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
        },
      );

      final notifier =
          container.read(configMapWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'cfg',
            namespace: 'default',
            data: const [KeyValuePair(key: 'k', value: 'v')],
          ));
      await notifier.next();
      expect(container.read(configMapWizardProvider(_key)).currentStep, 1);

      notifier.back();
      final state = container.read(configMapWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.status, WizardStatus.formEditing);
      expect(state.previewYaml, isNull);
    });
  });
}
