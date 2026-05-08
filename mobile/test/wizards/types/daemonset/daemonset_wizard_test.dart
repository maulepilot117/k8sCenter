// Tests for DaemonSetWizardController.

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/daemonset/daemonset_wizard_controller.dart';
import 'package:kubecenter/wizards/widgets/key_value_table.dart';
import 'package:kubecenter/wizards/widgets/probe_form.dart';
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
  return container.listen(daemonSetWizardProvider(_key), (_, _) {});
}

void main() {
  group('DaemonSetWizardController', () {
    test('toPreviewBody nests probes under container, omits empty probes',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(daemonSetWizardProvider(_key).notifier);
      const minimal = DaemonSetForm(
        name: 'ds',
        namespace: 'kube-system',
        image: 'busybox',
      );
      final minBody = c.toPreviewBody(minimal);
      expect((minBody['container'] as Map).containsKey('probes'), false);

      const withProbes = DaemonSetForm(
        name: 'ds',
        namespace: 'kube-system',
        image: 'busybox',
        liveness: ProbeData(type: 'http', path: '/healthz', port: 8080),
      );
      final body = c.toPreviewBody(withProbes);
      final probes = (body['container'] as Map)['probes'] as Map;
      expect(probes.containsKey('liveness'), true);
      expect(probes.containsKey('readiness'), false);
    });

    test('nodeSelectorAsMap drops empty rows', () {
      const form = DaemonSetForm(
        nodeSelector: [
          KeyValuePair(
              key: 'node-role.kubernetes.io/worker', value: 'true'),
          KeyValuePair(key: '', value: 'leftover'),
        ],
      );
      expect(form.nodeSelectorAsMap(), {
        'node-role.kubernetes.io/worker': 'true',
      });
    });

    test('happy path with nodeSelector previews', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/daemonset/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: apps/v1\nkind: DaemonSet\nspec:\n  template:\n    spec:\n      nodeSelector:\n        node-role.kubernetes.io/worker: "true"\n',
          },
        },
      );

      final notifier =
          container.read(daemonSetWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'ds',
            namespace: 'kube-system',
            image: 'busybox:latest',
            nodeSelector: const [
              KeyValuePair(
                  key: 'node-role.kubernetes.io/worker', value: 'true'),
            ],
          ));
      await notifier.next();

      final state = container.read(daemonSetWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('nodeSelector'));
    });

    test('422 with field=container.image rewinds to Configure', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/daemonset/preview',
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

      final notifier =
          container.read(daemonSetWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'ds',
            namespace: 'kube-system',
            image: 'X',
          ));
      await notifier.next();

      final state = container.read(daemonSetWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['container.image'], 'is required');
    });
  });
}
