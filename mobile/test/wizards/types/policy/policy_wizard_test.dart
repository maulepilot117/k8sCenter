// Tests for PolicyWizardController via the generic WizardController
// state machine.
//
// Covers:
//   - Initial state: formEditing step 0, empty templateId,
//     default excludedNamespaces = ['kube-system', 'kube-public', 'kube-node-lease'].
//   - pickTemplate auto-fills name, targetKinds, description, action (kyverno default).
//   - pickTemplate PRESERVES a pre-set engine when the template supports it.
//   - pickTemplate with pre-set gatekeeper engine computes gatekeeper default action.
//   - setEngine re-defaults action from 'Enforce' to 'deny' for gatekeeper.
//   - validateLocally step 0 blocks when templateId empty.
//   - validateLocally step 1 blocks when name/engine/action/targetKinds blank.
//   - validateLocally step 1 required-param gate: allowed-registries with empty registries.
//   - errorRouter maps paths to correct step indices.
//   - toPreviewBody: omits description when blank, omits params when empty.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/policy/policy_wizard_controller.dart';
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
  return container.listen(policyWizardProvider(_key), (_, _) {});
}

void main() {
  group('PolicyWizardController', () {
    test('initial state: formEditing step 0, empty templateId, default excludedNamespaces',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(policyWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.form.templateId, isEmpty);
      expect(state.form.excludedNamespaces,
          containsAll(['kube-system', 'kube-public', 'kube-node-lease']));
    });

    test('pickTemplate: auto-fills name, targetKinds, description, kyverno action',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(policyWizardProvider(_key).notifier);
      notifier.pickTemplate('disallow-privileged');

      final form = container.read(policyWizardProvider(_key)).form;
      expect(form.templateId, 'disallow-privileged');
      expect(form.name, 'disallow-privileged');
      expect(form.targetKinds, contains('Pod'));
      expect(form.description, isNotEmpty);
      // 'disallow-privileged' supports kyverno + gatekeeper; first engine is kyverno.
      expect(form.engine, 'kyverno');
      expect(form.action, 'Enforce');
    });

    test('pickTemplate: does not overwrite a non-empty name already set', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(policyWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'my-custom-name'));
      notifier.pickTemplate('disallow-privileged');

      final form = container.read(policyWizardProvider(_key)).form;
      expect(form.name, 'my-custom-name',
          reason: 'pickTemplate must not overwrite a name the operator already typed');
    });

    test('pickTemplate PRESERVES pre-set engine when template supports it', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(policyWizardProvider(_key).notifier);
      // Pre-set engine to gatekeeper before picking the template.
      notifier.updateForm((f) => f.copyWith(engine: 'gatekeeper'));
      notifier.pickTemplate('disallow-privileged');

      final form = container.read(policyWizardProvider(_key)).form;
      expect(form.engine, 'gatekeeper',
          reason: 'Pre-set engine must be preserved when the template supports it');
    });

    test(
        'pickTemplate with pre-set gatekeeper engine computes gatekeeper default action',
        () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(policyWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(engine: 'gatekeeper'));
      notifier.pickTemplate('disallow-privileged');

      final form = container.read(policyWizardProvider(_key)).form;
      expect(form.action, 'deny',
          reason: 'Gatekeeper default for disallow-privileged is deny');
    });

    test('setEngine re-defaults action from Enforce to deny for gatekeeper', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(policyWizardProvider(_key).notifier);
      notifier.pickTemplate('disallow-privileged'); // sets engine=kyverno, action=Enforce
      expect(
          container.read(policyWizardProvider(_key)).form.action, 'Enforce');

      notifier.setEngine('gatekeeper');

      final form = container.read(policyWizardProvider(_key)).form;
      expect(form.engine, 'gatekeeper');
      expect(form.action, 'deny');
    });

    test('validateLocally step 0 blocks when templateId empty', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(policyWizardProvider(_key).notifier);
      await notifier.next();

      final state = container.read(policyWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['templateId'], isNotNull);
    });

    test('validateLocally step 1 blocks when name/engine/action/targetKinds blank',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(policyWizardProvider(_key).notifier);
      // Pick a template to pass step 0 — name auto-fills, so we clear it.
      notifier.pickTemplate('disallow-privileged');
      notifier.updateForm((f) => f.copyWith(
            name: '',
            engine: '',
            action: '',
            targetKinds: const [],
          ));
      await notifier.next(); // passes step 0 (templateId now set)
      expect(container.read(policyWizardProvider(_key)).currentStep, 1);

      await notifier.next(); // attempt step 1 → should fail

      final state = container.read(policyWizardProvider(_key));
      expect(state.currentStep, 1);
      expect(state.stepErrors[1]?['name'], isNotNull);
      expect(state.stepErrors[1]?['engine'], isNotNull);
      expect(state.stepErrors[1]?['action'], isNotNull);
      expect(state.stepErrors[1]?['targetKinds'], isNotNull);
    });

    test(
        'validateLocally step 1 required-param gate: allowed-registries with empty registries',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(policyWizardProvider(_key).notifier);
      notifier.pickTemplate('allowed-registries');
      // Clear the params to simulate an operator who did not fill in registries.
      notifier.updateForm((f) => f.copyWith(params: const {'registries': <String>[]}));
      await notifier.next(); // step 0 → 1
      await notifier.next(); // attempt step 1 — should fail on params.registries

      final state = container.read(policyWizardProvider(_key));
      expect(state.currentStep, 1);
      expect(state.stepErrors[1]?['params.registries'], isNotNull,
          reason: 'Empty registries list must be rejected by the required-param gate');
    });

    test('errorRouter maps paths to correct step indices', () {
      final c = PolicyWizardController();
      expect(c.errorRouter('templateId'), 0);
      expect(c.errorRouter('engine'), 1);
      expect(c.errorRouter('name'), 1);
      expect(c.errorRouter('action'), 1);
      expect(c.errorRouter('targetKinds'), 1);
      expect(c.errorRouter('description'), 1);
      expect(c.errorRouter('excludedNamespaces'), 1);
      expect(c.errorRouter('params.registries'), 1);
      expect(c.errorRouter('params.registries[0]'), 1);
      expect(c.errorRouter('totally-unknown-path'), isNull);
    });

    test('toPreviewBody: omits description when blank, omits params when empty', () {
      const form = PolicyForm(
        templateId: 'disallow-privileged',
        engine: 'kyverno',
        name: 'no-priv',
        action: 'Enforce',
        targetKinds: ['Pod'],
        excludedNamespaces: ['kube-system'],
        description: '',
        params: {},
      );
      final body = PolicyWizardController().toPreviewBody(form);
      expect(body['templateId'], 'disallow-privileged');
      expect(body['engine'], 'kyverno');
      expect(body['name'], 'no-priv');
      expect(body['action'], 'Enforce');
      expect(body['targetKinds'], ['Pod']);
      expect(body['excludedNamespaces'], ['kube-system']);
      expect(body.containsKey('description'), isFalse);
      expect(body.containsKey('params'), isFalse);
    });

    test('toPreviewBody: emits description when non-blank, emits params when set', () {
      final form = PolicyForm(
        templateId: 'allowed-registries',
        engine: 'kyverno',
        name: 'allowed-registries',
        action: 'Enforce',
        targetKinds: const ['Pod'],
        description: 'Custom description',
        params: const {'registries': ['ghcr.io/']},
      );
      final body = PolicyWizardController().toPreviewBody(form);
      expect(body['description'], 'Custom description');
      expect(body['params'], {'registries': ['ghcr.io/']});
    });

    test('happy path: valid form reaches reviewing', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/policy/preview',
        body: {
          'data': {
            'yaml': 'apiVersion: kyverno.io/v1\nkind: ClusterPolicy\n'
          },
        },
      );

      final notifier = container.read(policyWizardProvider(_key).notifier);
      notifier.pickTemplate('disallow-privileged');
      await notifier.next(); // step 0 → 1
      await notifier.next(); // step 1 → preview → reviewing

      final state = container.read(policyWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('ClusterPolicy'));
    });

    test('422 with templateId routes back to step 0', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/policy/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': '[{"field":"templateId","message":"unknown template"}]',
          }
        },
      );

      final notifier = container.read(policyWizardProvider(_key).notifier);
      notifier.pickTemplate('disallow-privileged');
      await notifier.next();
      await notifier.next();

      final state = container.read(policyWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['templateId'], contains('unknown'));
    });
  });
}
