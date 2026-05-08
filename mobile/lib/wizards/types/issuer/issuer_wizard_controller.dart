// Issuer / ClusterIssuer wizard controller.
//
// Mirrors `frontend/islands/IssuerWizard.tsx` and ports the wire contract
// from `backend/internal/wizard/issuer.go:55`.
//
// Scope-variant pattern: a single form/UI shape is shared between
// namespaced and cluster-scoped issuers. The two only differ in:
//   1. The backend wizard `:type` (`issuer` vs `cluster-issuer`)
//   2. Whether the namespace field is required and emitted
//   3. Whether the screen renders a namespace input
//
// Two concrete controllers ([IssuerWizardController] /
// [ClusterIssuerWizardController]) each provide their wizard type and
// scope, but share the [_IssuerWizardBase] state machine, validators,
// and serialization. Two thin subclasses keep the
// AutoDisposeNotifierProvider.family typing clean — the family value
// type can't carry constructor arguments.
//
// Wire format (`IssuerInput`):
//   { name, namespace?, type: "selfSigned" | "acme",
//     selfSigned?: {},
//     acme?: { server, email, privateKeySecretRefName,
//              solvers: [ { http01Ingress: { ingressClassName? } } ] } }
//
// Backend invariant: exactly one of `selfSigned` / `acme` is populated and
// matches `type`. We always emit the right body for the picked type.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_registry.dart';
import '../../wizard_step.dart';

enum IssuerType { selfSigned, acme }

/// Shorthand identifiers for the canonical ACME endpoints. The backend
/// only accepts HTTPS public URLs, so all three are HTTPS.
class AcmeServerPreset {
  const AcmeServerPreset({required this.label, required this.url});
  final String label;
  final String url;
}

const List<AcmeServerPreset> kAcmePresets = [
  AcmeServerPreset(
    label: "Let's Encrypt (production)",
    url: 'https://acme-v02.api.letsencrypt.org/directory',
  ),
  AcmeServerPreset(
    label: "Let's Encrypt (staging)",
    url: 'https://acme-staging-v02.api.letsencrypt.org/directory',
  ),
];

class AcmeSolver {
  const AcmeSolver({this.ingressClassName = ''});

  /// HTTP01 ingress class name. Empty leaves the field off (cert-manager
  /// uses the cluster's default ingress class). v1 only supports HTTP01;
  /// DNS01 is rejected by the backend.
  final String ingressClassName;

  AcmeSolver copyWith({String? ingressClassName}) =>
      AcmeSolver(ingressClassName: ingressClassName ?? this.ingressClassName);

  Map<String, dynamic> toJson() {
    final ingress = <String, dynamic>{};
    if (ingressClassName.trim().isNotEmpty) {
      ingress['ingressClassName'] = ingressClassName.trim();
    }
    return {
      'http01Ingress': ingress,
    };
  }
}

class AcmeForm {
  const AcmeForm({
    this.server = 'https://acme-v02.api.letsencrypt.org/directory',
    this.email = '',
    this.privateKeySecretRefName = '',
    this.solvers = const [AcmeSolver()],
  });

  final String server;
  final String email;
  final String privateKeySecretRefName;
  final List<AcmeSolver> solvers;

  AcmeForm copyWith({
    String? server,
    String? email,
    String? privateKeySecretRefName,
    List<AcmeSolver>? solvers,
  }) =>
      AcmeForm(
        server: server ?? this.server,
        email: email ?? this.email,
        privateKeySecretRefName:
            privateKeySecretRefName ?? this.privateKeySecretRefName,
        solvers: solvers ?? this.solvers,
      );

  Map<String, dynamic> toJson() => {
        'server': server,
        'email': email,
        'privateKeySecretRefName': privateKeySecretRefName,
        'solvers': [for (final s in solvers) s.toJson()],
      };
}

class IssuerForm {
  const IssuerForm({
    this.name = '',
    this.namespace = '',
    this.type = IssuerType.selfSigned,
    this.acme = const AcmeForm(),
  });

  final String name;
  final String namespace;
  final IssuerType type;

  /// Always tracked even when [type] is selfSigned, so an operator
  /// flipping back to ACME doesn't lose their entries.
  final AcmeForm acme;

  IssuerForm copyWith({
    String? name,
    String? namespace,
    IssuerType? type,
    AcmeForm? acme,
  }) =>
      IssuerForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        type: type ?? this.type,
        acme: acme ?? this.acme,
      );
}

abstract class _IssuerWizardBase extends WizardController<IssuerForm> {
  /// Set by the concrete subclass: namespaced vs cluster.
  WizardScope get scope;

  @override
  String get resourceListKind =>
      scope == WizardScope.cluster ? 'clusterissuers' : 'issuers';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Type',
          description: 'SelfSigned or ACME',
        ),
        WizardStep(
          title: 'Configure',
          description: 'Identity and issuer-specific fields',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  IssuerForm buildInitialForm() => const IssuerForm();

  @override
  Map<String, dynamic> toPreviewBody(IssuerForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'type': form.type == IssuerType.acme ? 'acme' : 'selfSigned',
    };
    if (scope != WizardScope.cluster) {
      body['namespace'] = form.namespace;
    }
    if (form.type == IssuerType.selfSigned) {
      // Backend wants `selfSigned: {}` to mark the body shape. Empty
      // map suffices.
      body['selfSigned'] = <String, dynamic>{};
    } else {
      body['acme'] = form.acme.toJson();
    }
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'type' || fieldPath == 'selfSigned') return 0;
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'scope' ||
        fieldPath == 'acme' ||
        fieldPath.startsWith('acme.')) {
      return 1;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(IssuerForm form, int stepIndex) {
    if (stepIndex == 1) {
      // Configure step: name + namespace (when applicable) + per-type
      // required fields.
      final out = <String, String>{
        ...validateNameAndNamespace(
          form.name,
          form.namespace,
          requireNamespace: scope != WizardScope.cluster,
        ),
      };
      if (form.type == IssuerType.acme) {
        if (form.acme.email.trim().isEmpty) {
          out['acme.email'] = 'Email is required';
        }
        if (form.acme.privateKeySecretRefName.trim().isEmpty) {
          out['acme.privateKeySecretRefName'] =
              'Private key secret name is required';
        }
        if (form.acme.solvers.isEmpty) {
          out['acme.solvers'] = 'Add at least one solver';
        }
      }
      return out;
    }
    return const <String, String>{};
  }
}

class IssuerWizardController extends _IssuerWizardBase {
  @override
  String get wizardType => 'issuer';

  @override
  WizardScope get scope => WizardScope.namespaced;
}

class ClusterIssuerWizardController extends _IssuerWizardBase {
  @override
  String get wizardType => 'cluster-issuer';

  @override
  WizardScope get scope => WizardScope.cluster;
}

final issuerWizardProvider = AutoDisposeNotifierProvider.family<
    IssuerWizardController, WizardState<IssuerForm>, WizardKey>(
  IssuerWizardController.new,
);

final clusterIssuerWizardProvider = AutoDisposeNotifierProvider.family<
    ClusterIssuerWizardController, WizardState<IssuerForm>, WizardKey>(
  ClusterIssuerWizardController.new,
);
