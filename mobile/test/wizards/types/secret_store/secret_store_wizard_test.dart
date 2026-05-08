// Tests for SecretStoreWizardController + ClusterSecretStoreWizardController
// via the generic WizardController state machine.
//
// Covers:
//   - Initial state: formEditing on step 0, empty name+namespace.
//   - Namespaced scope: validateLocally requires namespace; cluster scope does not.
//   - validateLocally step 1 blocks advance when provider is empty.
//   - switchProvider resets providerSpec to {} and updates provider id.
//   - toPreviewBody namespaced scope includes namespace; cluster scope omits it.
//   - toPreviewBody omits refreshInterval when empty; includes it when set.
//   - 422 routing: auth.kubernetes.role → step 2, provider → step 1, name → step 0.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/secret_store/secret_store_wizard_controller.dart';
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
  return container.listen(secretStoreWizardProvider(_key), (_, _) {});
}

ProviderSubscription _keepAliveCluster(ProviderContainer container) {
  return container.listen(clusterSecretStoreWizardProvider(_key), (_, _) {});
}

void main() {
  group('SecretStoreWizardController (namespaced)', () {
    test('initial state: formEditing on step 0, empty name + namespace', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.form.name, isEmpty);
      expect(state.form.namespace, isEmpty);
      expect(state.form.provider, isEmpty);
      expect(state.form.providerSpec, isEmpty);
    });

    test('validateLocally step 0 blocks: name AND namespace required', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      await notifier.next();

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
    });

    test('validateLocally step 1 blocks when provider empty', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      // Fill step 0 so we can advance.
      notifier.updateForm((f) => f.copyWith(name: 'my-store', namespace: 'app'));
      await notifier.next(); // advance to step 1
      expect(container.read(secretStoreWizardProvider(_key)).currentStep, 1);

      // Now try to advance with empty provider.
      await notifier.next();

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.currentStep, 1);
      expect(state.stepErrors[1]?['provider'], isNotNull);
    });

    test('switchProvider resets providerSpec AND sets new provider id', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      // Seed the form with vault + some spec data to simulate stale state.
      notifier.updateForm((f) => f.copyWith(
            provider: 'vault',
            providerSpec: const {
              'server': 'https://vault.example.com',
              'auth': {
                'token': {
                  'tokenSecretRef': {'name': 'vault-token', 'key': 'token'}
                }
              },
            },
          ));

      // Switch to AWS — the Vault spec must be wiped.
      notifier.switchProvider('aws');

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.form.provider, 'aws');
      expect(state.form.providerSpec, isEmpty,
          reason: 'providerSpec must be reset on provider switch so stale '
              'Vault credentials cannot leak into the AWS form');
    });

    test('switchProvider is a no-op when provider already matches', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            provider: 'vault',
            providerSpec: const {'server': 'https://vault.example.com'},
          ));

      notifier.switchProvider('vault'); // same provider — noop

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.form.provider, 'vault');
      // Spec was NOT cleared.
      expect(state.form.providerSpec['server'], 'https://vault.example.com');
    });

    test('toPreviewBody (namespaced): includes namespace, emits refreshInterval when set', () {
      final form = const SecretStoreForm(
        name: 'vault-store',
        namespace: 'app',
        provider: 'vault',
        refreshInterval: '30m',
        providerSpec: {'server': 'https://vault.example.com'},
      );
      final body = SecretStoreWizardController().toPreviewBody(form);
      expect(body['name'], 'vault-store');
      expect(body['namespace'], 'app');
      expect(body['provider'], 'vault');
      expect(body['providerSpec'], {'server': 'https://vault.example.com'});
      expect(body['refreshInterval'], '30m');
    });

    test('toPreviewBody (namespaced): omits refreshInterval when empty', () {
      const form = SecretStoreForm(
        name: 'vault-store',
        namespace: 'app',
        provider: 'vault',
        refreshInterval: '',
        providerSpec: {},
      );
      final body = SecretStoreWizardController().toPreviewBody(form);
      expect(body.containsKey('refreshInterval'), isFalse);
    });

    test('happy path: namespaced store reaches reviewing on 200 preview', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/secret-store/preview',
        body: {
          'data': {
            'yaml': 'apiVersion: external-secrets.io/v1beta1\nkind: SecretStore\n'
          },
        },
      );

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'vault-store', namespace: 'app'));
      await notifier.next(); // step 0 → step 1
      notifier.updateForm((f) => f.copyWith(
            provider: 'vault',
            providerSpec: const {
              'server': 'https://vault.example.com',
              'auth': {'token': {'tokenSecretRef': {'name': 'tok', 'key': 'token'}}},
            },
          ));
      await notifier.next(); // step 1 → step 2
      await notifier.next(); // step 2 → preview → reviewing

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('SecretStore'));
    });

    test('422 with auth.kubernetes.role path routes to step 2 (Configure)', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/secret-store/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail':
                '[{"field":"auth.kubernetes.role","message":"role is required for kubernetes auth"}]',
          }
        },
      );

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'k8s-store', namespace: 'app'));
      await notifier.next();
      notifier.updateForm((f) => f.copyWith(
            provider: 'vault',
            providerSpec: const <String, dynamic>{
              'server': 'https://vault.example.com',
              'auth': <String, dynamic>{'kubernetes': <String, dynamic>{}},
            },
          ));
      await notifier.next();
      await notifier.next();

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 2);
      expect(state.stepErrors[2]?['auth.kubernetes.role'], contains('required'));
    });

    test('422 with provider path routes to step 1', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/secret-store/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': '[{"field":"provider","message":"unknown provider"}]',
          }
        },
      );

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'store', namespace: 'app'));
      await notifier.next();
      notifier.updateForm((f) => f.copyWith(
            provider: 'vault',
            providerSpec: const {'server': 'https://vault.example.com'},
          ));
      await notifier.next();
      await notifier.next();

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.currentStep, 1);
      expect(state.stepErrors[1]?['provider'], contains('unknown'));
    });

    test('422 with name path routes to step 0', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/secret-store/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': '[{"field":"name","message":"name already exists"}]',
          }
        },
      );

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'store', namespace: 'app'));
      await notifier.next();
      notifier.updateForm((f) => f.copyWith(
            provider: 'vault',
            providerSpec: const {'server': 'https://vault.example.com'},
          ));
      await notifier.next();
      await notifier.next();

      final state = container.read(secretStoreWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], contains('exists'));
    });
  });

  group('ClusterSecretStoreWizardController (cluster-scope)', () {
    test('initial state: formEditing on step 0, empty name+namespace', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAliveCluster(container);
      addTearDown(sub.close);

      final state = container.read(clusterSecretStoreWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.form.name, isEmpty);
    });

    test('validateLocally step 0: cluster scope does NOT require namespace', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAliveCluster(container);
      addTearDown(sub.close);

      final notifier =
          container.read(clusterSecretStoreWizardProvider(_key).notifier);
      await notifier.next(); // empty name — should only error on name

      final state = container.read(clusterSecretStoreWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNull);
    });

    test('toPreviewBody (cluster): omits namespace', () {
      const form = SecretStoreForm(
        name: 'shared-store',
        namespace: '',
        provider: 'vault',
        providerSpec: {'server': 'https://vault.example.com'},
      );
      final body = ClusterSecretStoreWizardController().toPreviewBody(form);
      expect(body['name'], 'shared-store');
      expect(body.containsKey('namespace'), isFalse);
      expect(body['provider'], 'vault');
    });

    test('cluster: posts to /wizards/cluster-secret-store/preview', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAliveCluster(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/cluster-secret-store/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: external-secrets.io/v1beta1\nkind: ClusterSecretStore\n'
          },
        },
      );

      final notifier =
          container.read(clusterSecretStoreWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'shared-store'));
      await notifier.next(); // step 0 → step 1
      notifier.updateForm((f) => f.copyWith(
            provider: 'vault',
            providerSpec: const {'server': 'https://vault.example.com'},
          ));
      await notifier.next(); // step 1 → step 2
      await notifier.next(); // step 2 → preview → reviewing

      final state = container.read(clusterSecretStoreWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);

      final req = mock.requests.last;
      expect(req.path, '/api/v1/wizards/cluster-secret-store/preview');
      final body = req.data as Map<String, dynamic>;
      expect(body['name'], 'shared-store');
      expect(body.containsKey('namespace'), isFalse);
    });
  });

  group('errorRouter', () {
    test('routes expected paths to correct step indices', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(secretStoreWizardProvider(_key).notifier);
      expect(notifier.errorRouter('name'), 0);
      expect(notifier.errorRouter('namespace'), 0);
      expect(notifier.errorRouter('refreshInterval'), 0);
      expect(notifier.errorRouter('scope'), 0);
      expect(notifier.errorRouter('provider'), 1);
      expect(notifier.errorRouter('providerSpec'), 1);
      expect(notifier.errorRouter('auth.kubernetes.role'), 2);
      expect(notifier.errorRouter('server'), 2);
      expect(notifier.errorRouter('region'), 2);
    });
  });
}
