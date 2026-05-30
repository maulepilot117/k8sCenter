// Regression suite for the post-PR-3a-review hardening pass on
// WizardController:
//
//   * Partial-apply (summary.failed > 0) is treated as failed, not
//     applied.
//   * Preview cluster-mismatch transitions to failed with
//     clusterMismatch=true.
//   * Unknown 422 field paths land in state.unrouted, not silently in
//     step 0.
//   * next() guards against rapid double-tap during preview.
//   * back() during in-flight preview is preserved when the late 200
//     arrives (dispatch-id supersedes the result).
//
// Uses the ConfigMap controller as the concrete subclass — every
// hardening fix lives in the base class so any subclass exercises the
// same paths.

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

ProviderSubscription _keepAlive(ProviderContainer container) =>
    container.listen(configMapWizardProvider(_key), (_, _) {});

ConfigMapWizardController _ctl(ProviderContainer c) =>
    c.read(configMapWizardProvider(_key).notifier);

ConfigMapForm _filledForm() => const ConfigMapForm(
      name: 'cfg',
      namespace: 'default',
      data: [KeyValuePair(key: 'k', value: 'v')],
    );

void main() {
  group('WizardController hardening', () {
    test('partial-apply (summary.failed > 0) transitions to failed', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson('POST', '/api/v1/wizards/configmap/preview', body: {
        'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
      });
      mock.onJson('POST', '/api/v1/yaml/apply', body: {
        'data': {
          'results': [
            {
              'index': 0,
              'kind': 'ResourceQuota',
              'name': 'cfg-quota',
              'namespace': 'default',
              'action': 'created',
            },
            {
              'index': 1,
              'kind': 'LimitRange',
              'name': 'cfg-limits',
              'namespace': 'default',
              'action': 'failed',
              'error': 'invalid limit value',
            },
          ],
          'summary': {
            'total': 2,
            'created': 1,
            'configured': 0,
            'unchanged': 0,
            'failed': 1,
          },
        },
      });

      final notifier = _ctl(container);
      notifier.updateForm((_) => _filledForm());
      await notifier.next();
      await notifier.apply();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.failed,
          reason: 'partial failure must NOT transition to applied');
      expect(state.applyOutcome, isNotNull);
      expect(state.applyOutcome!.failed, 1);
      expect(state.errorMessage, contains('partially failed'));
      // Preview YAML preserved so operator can Back-edit-retry.
      expect(state.previewYaml, isNotNull);
    });

    test('preview cluster-mismatch transitions to failed with clusterMismatch',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson('POST', '/api/v1/wizards/configmap/preview', body: {
        'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
      });

      final notifier = _ctl(container);
      notifier.updateForm((_) => _filledForm());

      // Switch the active cluster BEFORE next() runs preview. The
      // entry-time pin check catches it and transitions to failed.
      container.read(activeClusterProvider.notifier).setCluster('other');
      await notifier.next();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.failed);
      expect(state.clusterMismatch, isTrue);
      expect(state.errorMessage, contains('Cluster changed'));
    });

    test(
        'preview returns YAML but cluster switched during HTTP — '
        'late-arrival pin check aborts', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson('POST', '/api/v1/wizards/configmap/preview', body: {
        'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
      });

      final notifier = _ctl(container);
      notifier.updateForm((_) => _filledForm());
      // Schedule a cluster switch to fire on the next microtask, so
      // it lands AFTER the request issues but BEFORE the result is
      // applied to state. The late-arrival check in _runPreview
      // catches it.
      Future.microtask(() {
        container.read(activeClusterProvider.notifier).setCluster('other');
      });
      await notifier.next();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.failed);
      expect(state.clusterMismatch, isTrue);
    });

    test('unknown 422 field path lands in state.unrouted, not step 0',
        () async {
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
              {
                'field': 'spec.unknownField',
                'message': 'this is something the wizard does not know about'
              },
              {'field': 'name', 'message': 'must be a valid DNS label'},
            ]),
          },
        },
      );

      final notifier = _ctl(container);
      notifier.updateForm((_) => _filledForm());
      await notifier.next();

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      // Known path landed in step 0 errors.
      expect(state.stepErrors[0]?['name'], contains('DNS label'));
      // Unknown path landed in unrouted, NOT in step 0.
      expect(state.unrouted['spec.unknownField'], isNotNull);
      expect(state.stepErrors[0]?.containsKey('spec.unknownField'), isFalse);
    });

    test('next() during previewing is a no-op (rapid double-tap guard)',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      // First call: succeed; second concurrent call: should be ignored
      // by the previewing guard. We assert via request count.
      mock.onJson('POST', '/api/v1/wizards/configmap/preview', body: {
        'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
      });

      final notifier = _ctl(container);
      notifier.updateForm((_) => _filledForm());
      // Fire two next()s back-to-back.
      final f1 = notifier.next();
      final f2 = notifier.next();
      await Future.wait([f1, f2]);

      // Only one preview request reached the mock.
      final previewRequests = mock.requests
          .where((r) => r.path == '/api/v1/wizards/configmap/preview')
          .toList();
      expect(previewRequests.length, 1);
      // And the wizard ended up at reviewing.
      expect(container.read(configMapWizardProvider(_key)).status,
          WizardStatus.reviewing);
    });

    test(
        'updateForm during in-flight preview supersedes the late 200 — '
        'wizard stays at the form step', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      // Standard JSON mock — the late-arrival guard fires before the
      // result is applied to state, so request-shape is irrelevant.
      mock.onJson('POST', '/api/v1/wizards/configmap/preview', body: {
        'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
      });

      final notifier = _ctl(container);
      notifier.updateForm((_) => _filledForm());

      // Start preview, then bump dispatch id mid-flight by editing
      // the form. updateForm bumps _dispatchId synchronously; the
      // preview's awaited response then fails the dispatch-id
      // equality check on resume and drops the result.
      final previewFuture = notifier.next();
      notifier.updateForm((f) => f.copyWith(name: 'changed'));
      await previewFuture;

      final state = container.read(configMapWizardProvider(_key));
      // Late preview's 200 dropped — wizard stayed at form editing.
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      // Form carries the post-bump value, not the original.
      expect(state.form.name, 'changed');
    });

    test(
        'goToStep out of Review resets status to formEditing and clears '
        'the stale preview YAML', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson('POST', '/api/v1/wizards/configmap/preview', body: {
        'data': {'yaml': 'apiVersion: v1\nkind: ConfigMap\n'},
      });

      final notifier = _ctl(container);
      notifier.updateForm((_) => _filledForm());
      // Run preview to land at Review with a populated preview YAML.
      await notifier.next();

      final reviewing = container.read(configMapWizardProvider(_key));
      expect(reviewing.status, WizardStatus.reviewing);
      expect(reviewing.previewYaml, isNotNull);

      // Operator taps a completed-step chip to jump back to the form
      // step. This must reset status and drop the stale preview YAML so
      // the footer can no longer offer Apply on un-previewed input.
      notifier.goToStep(0);

      final state = container.read(configMapWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.previewYaml, isNull);
      expect(state.currentStep, 0);
    });
  });
}
