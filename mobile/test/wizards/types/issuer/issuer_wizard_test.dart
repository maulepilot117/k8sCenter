// Tests for IssuerWizardController + ClusterIssuerWizardController.
//
// Covers:
//   - Initial state: SelfSigned default, formEditing on step 0.
//   - SelfSigned happy path posts to /wizards/issuer/preview with
//     selfSigned: {} body and namespace set.
//   - Cluster scope omits namespace from the preview body and posts
//     to /wizards/cluster-issuer/preview.
//   - ACME validateLocally flags missing email + privateKeySecretRefName
//     on step 1.
//   - ACME happy path emits server, email, privateKeySecretRefName,
//     and one HTTP01 solver.
//   - 422 with `acme.email` routes back to Configure step (index 1).
//   - errorRouter routes scope-bound paths to step 1 and `type` to step 0.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/issuer/issuer_wizard_controller.dart';
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

void main() {
  group('IssuerWizardController (namespaced)', () {
    test('initial state: SelfSigned, step 0', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = container.listen(issuerWizardProvider(_key), (_, _) {});
      addTearDown(sub.close);

      final state = container.read(issuerWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.form.type, IssuerType.selfSigned);
    });

    test('SelfSigned happy path: skipped step 0, completes at step 2', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = container.listen(issuerWizardProvider(_key), (_, _) {});
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/issuer/preview',
        body: {
          'data': {
            'yaml': 'apiVersion: cert-manager.io/v1\nkind: Issuer\n'
          },
        },
      );

      final n = container.read(issuerWizardProvider(_key).notifier);
      // Step 0 (Type) — already SelfSigned by default. Advance.
      await n.next();
      // Now on step 1. Fill name + namespace.
      n.updateForm((f) => f.copyWith(name: 'app-issuer', namespace: 'app'));
      await n.next();

      final state = container.read(issuerWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.currentStep, 2);
      expect(state.previewYaml, contains('Issuer'));

      // Verify the request body shape.
      final req = mock.requests.last;
      final body = req.data as Map<String, dynamic>;
      expect(body['type'], 'selfSigned');
      expect(body['namespace'], 'app');
      expect(body['name'], 'app-issuer');
      expect(body['selfSigned'], <String, dynamic>{});
      expect(body.containsKey('acme'), isFalse);
    });

    test('ACME: validateLocally flags missing email + privateKey ref',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = container.listen(issuerWizardProvider(_key), (_, _) {});
      addTearDown(sub.close);

      final n = container.read(issuerWizardProvider(_key).notifier);
      n.updateForm((f) => f.copyWith(type: IssuerType.acme));
      await n.next(); // advance from Type → Configure
      // Configure step now in focus. Try to advance with empty email/etc.
      n.updateForm((f) => f.copyWith(name: 'i', namespace: 'app'));
      await n.next();

      final state = container.read(issuerWizardProvider(_key));
      expect(state.currentStep, 1);
      expect(state.stepErrors[1]?['acme.email'], isNotNull);
      expect(state.stepErrors[1]?['acme.privateKeySecretRefName'], isNotNull);
    });

    test('ACME happy path: emits server, email, privateKey, solvers', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = container.listen(issuerWizardProvider(_key), (_, _) {});
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/issuer/preview',
        body: {
          'data': {'yaml': 'apiVersion: cert-manager.io/v1\nkind: Issuer\n'},
        },
      );

      final n = container.read(issuerWizardProvider(_key).notifier);
      n.updateForm((f) => f.copyWith(
            type: IssuerType.acme,
            name: 'le-staging',
            namespace: 'app',
            acme: const AcmeForm(
              server: 'https://acme-staging-v02.api.letsencrypt.org/directory',
              email: 'ops@example.com',
              privateKeySecretRefName: 'le-staging-key',
              solvers: [AcmeSolver(ingressClassName: 'nginx')],
            ),
          ));
      // Advance through all form steps.
      await n.next(); // Type → Configure
      await n.next(); // Configure → preview

      final state = container.read(issuerWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);

      final body = mock.requests.last.data as Map<String, dynamic>;
      expect(body['type'], 'acme');
      final acme = body['acme'] as Map<String, dynamic>;
      expect(acme['email'], 'ops@example.com');
      expect(acme['privateKeySecretRefName'], 'le-staging-key');
      expect(acme['server'], contains('acme-staging-v02'));
      final solvers = acme['solvers'] as List;
      expect(solvers, hasLength(1));
      final solver = solvers.first as Map<String, dynamic>;
      expect((solver['http01Ingress'] as Map)['ingressClassName'], 'nginx');
    });

    test('422 with acme.email path routes back to step 1', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = container.listen(issuerWizardProvider(_key), (_, _) {});
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/issuer/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail':
                '[{"field":"acme.email","message":"must be a valid email address"}]'
          }
        },
      );

      final n = container.read(issuerWizardProvider(_key).notifier);
      n.updateForm((f) => f.copyWith(
            type: IssuerType.acme,
            name: 'i',
            namespace: 'app',
            acme: const AcmeForm(
              email: 'ops@', // pass local validation, fail server
              privateKeySecretRefName: 'k',
              solvers: [AcmeSolver(ingressClassName: 'nginx')],
            ),
          ));
      await n.next();
      await n.next();

      final state = container.read(issuerWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 1);
      expect(
        state.stepErrors[1]?['acme.email'],
        contains('valid email'),
      );
    });
  });

  group('ClusterIssuerWizardController (cluster-scope)', () {
    test('omits namespace; posts to /wizards/cluster-issuer/preview',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub =
          container.listen(clusterIssuerWizardProvider(_key), (_, _) {});
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/cluster-issuer/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: cert-manager.io/v1\nkind: ClusterIssuer\n'
          },
        },
      );

      final n = container.read(clusterIssuerWizardProvider(_key).notifier);
      // Type defaults to selfSigned.
      await n.next();
      n.updateForm((f) => f.copyWith(name: 'self-signed-cluster'));
      await n.next();

      final state = container.read(clusterIssuerWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);

      final req = mock.requests.last;
      expect(req.path, '/api/v1/wizards/cluster-issuer/preview');
      final body = req.data as Map<String, dynamic>;
      expect(body['name'], 'self-signed-cluster');
      expect(body.containsKey('namespace'), isFalse);
      expect(body['selfSigned'], <String, dynamic>{});
    });

    test('cluster scope: validateLocally does not require namespace',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub =
          container.listen(clusterIssuerWizardProvider(_key), (_, _) {});
      addTearDown(sub.close);

      final n = container.read(clusterIssuerWizardProvider(_key).notifier);
      await n.next(); // Type → Configure
      // Empty name, empty namespace — only name should error.
      await n.next();
      final state = container.read(clusterIssuerWizardProvider(_key));
      expect(state.stepErrors[1]?['name'], isNotNull);
      expect(state.stepErrors[1]?['namespace'], isNull);
    });
  });

  group('errorRouter', () {
    test('routes name/namespace/acme.*/selfSigned to step 1; type to step 0',
        () {
      final c = IssuerWizardController();
      expect(c.errorRouter('type'), 0);
      // selfSigned renders in the step-1 Configure body (_SelfSignedSummary),
      // so its backend field error must route to step 1, not step 0.
      expect(c.errorRouter('selfSigned'), 1);
      expect(c.errorRouter('name'), 1);
      expect(c.errorRouter('namespace'), 1);
      expect(c.errorRouter('acme.email'), 1);
      expect(c.errorRouter('acme.solvers[0].http01Ingress.ingressClassName'), 1);
      expect(c.errorRouter('totally-unknown-path'), isNull);
    });
  });
}
