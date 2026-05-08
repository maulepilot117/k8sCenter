// Certificate wizard controller. Mirrors `frontend/islands/CertificateWizard.tsx`
// and ports the wire contract from `backend/internal/wizard/certificate.go:38`.
//
// Wire format (`CertificateInput`):
//   {
//     name, namespace, secretName,
//     issuerRef: { name, kind: "Issuer" | "ClusterIssuer", group? },
//     dnsNames?:  []string,
//     commonName?: string,
//     duration?:    string  (Go duration, default 2160h, min 1h),
//     renewBefore?: string  (Go duration, default 360h, min 5m, < duration),
//     privateKey?: { algorithm?, size?, rotationPolicy? },
//     isCA?: bool,
//   }
//
// Backend invariant: at least one of dnsNames or commonName must be set.
// privateKey.size constraints: RSA ∈ {2048,3072,4096}; ECDSA ∈ {256,384,521};
// Ed25519 must omit size. The form's Configure step prevents the
// algorithm/size mismatch by re-defaulting size on algorithm change.
//
// One Configure step + Review.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/issuer_picker.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<String> kCertPrivateKeyAlgorithms = ['RSA', 'ECDSA', 'Ed25519'];

/// Default size for each algorithm. Picked to match cert-manager's own
/// defaults so the operator landing on the form sees the same values
/// the backend would generate if the form omitted privateKey entirely.
const Map<String, int?> kCertDefaultKeySize = {
  'RSA': 2048,
  'ECDSA': 256,
  'Ed25519': null, // Ed25519 rejects a size — must be 0/omitted.
};

class CertPrivateKey {
  const CertPrivateKey({this.algorithm = 'RSA', this.size = 2048});

  final String algorithm;
  final int? size;

  CertPrivateKey copyWith({String? algorithm, int? size, bool clearSize = false}) {
    return CertPrivateKey(
      algorithm: algorithm ?? this.algorithm,
      size: clearSize ? null : (size ?? this.size),
    );
  }

  Map<String, dynamic> toJson() {
    final m = <String, dynamic>{'algorithm': algorithm};
    if (size != null) m['size'] = size;
    return m;
  }
}

class CertificateForm {
  const CertificateForm({
    this.name = '',
    this.namespace = '',
    this.secretName = '',
    this.issuerRef,
    this.dnsNames = const <String>[],
    this.commonName = '',
    this.duration = '',
    this.renewBefore = '',
    this.privateKey = const CertPrivateKey(),
  });

  final String name;
  final String namespace;
  final String secretName;
  final IssuerSelection? issuerRef;
  final List<String> dnsNames;
  final String commonName;
  final String duration;
  final String renewBefore;
  final CertPrivateKey privateKey;

  CertificateForm copyWith({
    String? name,
    String? namespace,
    String? secretName,
    IssuerSelection? issuerRef,
    List<String>? dnsNames,
    String? commonName,
    String? duration,
    String? renewBefore,
    CertPrivateKey? privateKey,
  }) {
    return CertificateForm(
      name: name ?? this.name,
      namespace: namespace ?? this.namespace,
      secretName: secretName ?? this.secretName,
      issuerRef: issuerRef ?? this.issuerRef,
      dnsNames: dnsNames ?? this.dnsNames,
      commonName: commonName ?? this.commonName,
      duration: duration ?? this.duration,
      renewBefore: renewBefore ?? this.renewBefore,
      privateKey: privateKey ?? this.privateKey,
    );
  }
}

class CertificateWizardController extends WizardController<CertificateForm> {
  @override
  String get wizardType => 'certificate';

  @override
  String get resourceListKind => 'certificates';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Identity, issuer, names, key',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  CertificateForm buildInitialForm() => const CertificateForm();

  @override
  Map<String, dynamic> toPreviewBody(CertificateForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'secretName': form.secretName,
      'issuerRef': {
        'name': form.issuerRef?.name ?? '',
        'kind': form.issuerRef?.kind ?? 'Issuer',
      },
      // privateKey is always emitted — the operator's algorithm+size
      // pick is part of the form contract and the backend validates it.
      'privateKey': form.privateKey.toJson(),
    };
    final dns = form.dnsNames.where((s) => s.trim().isNotEmpty).toList();
    if (dns.isNotEmpty) body['dnsNames'] = dns;
    if (form.commonName.trim().isNotEmpty) {
      body['commonName'] = form.commonName.trim();
    }
    if (form.duration.trim().isNotEmpty) {
      body['duration'] = form.duration.trim();
    }
    if (form.renewBefore.trim().isNotEmpty) {
      body['renewBefore'] = form.renewBefore.trim();
    }
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    // Every field on this wizard belongs to the Configure step (index 0).
    // List the canonical paths explicitly so the unrouted-banner branch
    // catches anything the backend introduces in a future server update
    // without silently merging it into step 0.
    const known = {
      'name',
      'namespace',
      'secretName',
      'issuerRef.name',
      'issuerRef.kind',
      'commonName',
      'duration',
      'renewBefore',
      'dnsNames',
      'privateKey.algorithm',
      'privateKey.size',
      'privateKey.rotationPolicy',
    };
    if (known.contains(fieldPath)) return 0;
    if (fieldPath.startsWith('dnsNames[')) return 0;
    if (fieldPath.startsWith('privateKey.')) return 0;
    return null;
  }

  @override
  StepFieldErrors validateLocally(CertificateForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
    if (form.secretName.trim().isEmpty) {
      out['secretName'] = 'Secret name is required';
    }
    final ref = form.issuerRef;
    if (ref == null || ref.name.isEmpty) {
      out['issuerRef.name'] = 'Issuer is required';
    }
    final hasDns = form.dnsNames.any((s) => s.trim().isNotEmpty);
    final hasCN = form.commonName.trim().isNotEmpty;
    if (!hasDns && !hasCN) {
      out['dnsNames'] = 'At least one DNS name or a common name is required';
    }
    return out;
  }
}

final certificateWizardProvider = AutoDisposeNotifierProvider.family<
    CertificateWizardController, WizardState<CertificateForm>, WizardKey>(
  CertificateWizardController.new,
);
