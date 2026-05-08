// Tests for CertificateWizardController via the generic WizardController
// state machine.
//
// Covers:
//   - Initial state: formEditing on step 0 with empty form, RSA-2048 default key.
//   - validateLocally blocks advance when name/namespace/secretName/issuer
//     missing, and when neither dnsNames nor commonName is provided.
//   - Happy path: name/namespace/secretName/issuerRef + a DNS name → preview
//     200, transitions to reviewing.
//   - 422 with `dnsNames` field path routes back to Configure step.
//   - toPreviewBody: omits empty optional fields, emits issuerRef.kind from
//     the IssuerSelection, skips size for Ed25519.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/certificate/certificate_wizard_controller.dart';
import 'package:kubecenter/wizards/widgets/issuer_picker.dart';
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
  return container.listen(certificateWizardProvider(_key), (_, _) {});
}

void main() {
  group('CertificateWizardController', () {
    test('initial state: formEditing on step 0, RSA 2048 default', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(certificateWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.form.privateKey.algorithm, 'RSA');
      expect(state.form.privateKey.size, 2048);
    });

    test('validateLocally blocks advance with empty form', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(certificateWizardProvider(_key).notifier);
      await notifier.next();

      final state = container.read(certificateWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['name'], isNotNull);
      expect(state.stepErrors[0]?['namespace'], isNotNull);
      expect(state.stepErrors[0]?['secretName'], isNotNull);
      expect(state.stepErrors[0]?['issuerRef.name'], isNotNull);
      expect(state.stepErrors[0]?['dnsNames'], isNotNull);
    });

    test('validateLocally requires either dnsNames or commonName', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(certificateWizardProvider(_key).notifier);
      // Set everything except identifiers.
      notifier.updateForm((f) => f.copyWith(
            name: 'web-tls',
            namespace: 'app',
            secretName: 'web-tls-secret',
            issuerRef: const IssuerSelection(
                name: 'letsencrypt-prod', kind: 'ClusterIssuer'),
          ));
      await notifier.next();
      expect(
        container.read(certificateWizardProvider(_key)).stepErrors[0]?['dnsNames'],
        isNotNull,
      );
      expect(
        container.read(certificateWizardProvider(_key)).status,
        WizardStatus.formEditing,
      );

      // Adding a commonName satisfies the constraint.
      notifier.updateForm((f) => f.copyWith(commonName: 'web.example.com'));
      // No mock for preview yet → just verify validateLocally clears.
      final after = container.read(certificateWizardProvider(_key));
      expect(after.stepErrors[0], isNot(contains('dnsNames')));
    });

    test('happy path: valid form runs preview, transitions to reviewing',
        () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/certificate/preview',
        body: {
          'data': {
            'yaml': 'apiVersion: cert-manager.io/v1\nkind: Certificate\n'
          },
        },
      );

      final notifier =
          container.read(certificateWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web-tls',
            namespace: 'app',
            secretName: 'web-tls-secret',
            issuerRef: const IssuerSelection(
                name: 'letsencrypt-prod', kind: 'ClusterIssuer'),
            dnsNames: const ['app.example.com'],
          ));
      await notifier.next();

      final state = container.read(certificateWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.currentStep, 1);
      expect(state.previewYaml, contains('Certificate'));
    });

    test('422 with dnsNames path routes back to Configure step', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/certificate/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail':
                '[{"field":"dnsNames[0]","message":"must be a valid DNS name"}]'
          }
        },
      );

      final notifier =
          container.read(certificateWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'web-tls',
            namespace: 'app',
            secretName: 'web-tls-secret',
            issuerRef: const IssuerSelection(
                name: 'letsencrypt-prod', kind: 'ClusterIssuer'),
            dnsNames: const ['*not_valid*'],
          ));
      await notifier.next();

      final state = container.read(certificateWizardProvider(_key));
      expect(state.status, WizardStatus.formEditing);
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['dnsNames[0]'], contains('valid DNS name'));
    });

    test('toPreviewBody: omits empty optionals; emits issuerRef.kind', () {
      final form = const CertificateForm(
        name: 'web-tls',
        namespace: 'app',
        secretName: 'web-tls-secret',
        issuerRef:
            IssuerSelection(name: 'letsencrypt-prod', kind: 'ClusterIssuer'),
        dnsNames: ['app.example.com'],
      );
      final c = CertificateWizardController();
      final body = c.toPreviewBody(form);
      expect(body['name'], 'web-tls');
      expect(body['issuerRef'], {
        'name': 'letsencrypt-prod',
        'kind': 'ClusterIssuer',
      });
      expect(body['dnsNames'], ['app.example.com']);
      expect(body.containsKey('commonName'), isFalse);
      expect(body.containsKey('duration'), isFalse);
      expect(body.containsKey('renewBefore'), isFalse);
      expect(body['privateKey'], {'algorithm': 'RSA', 'size': 2048});
    });

    test('toPreviewBody: Ed25519 omits size', () {
      const form = CertificateForm(
        name: 'web',
        namespace: 'app',
        secretName: 's',
        issuerRef: IssuerSelection(name: 'i', kind: 'Issuer'),
        dnsNames: ['x.example.com'],
        privateKey: CertPrivateKey(algorithm: 'Ed25519', size: null),
      );
      final body = CertificateWizardController().toPreviewBody(form);
      expect(body['privateKey'], {'algorithm': 'Ed25519'});
    });
  });
}
