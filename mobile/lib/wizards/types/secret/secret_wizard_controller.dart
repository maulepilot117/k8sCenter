// Secret wizard controller. Mirrors `frontend/islands/SecretWizard.tsx`.
//
// 2 steps: Configure (name + namespace + type + key-value rows) →
// Review (YAML preview + apply). Server validates DNS-1123, type
// enum, and type-specific required keys (e.g. tls.crt for TLS
// secrets).
//
// Wire format (`backend/internal/wizard/secret.go:11`):
//   { name, namespace, type, data: Map<String,String> }
//
// Values are typed as raw strings; the backend base64-encodes them on
// the way to k8s. Operators editing a Secret type that requires PEM or
// dockerconfigjson should paste the raw content — not pre-encoded —
// matching web behavior. The reveal toggle on the M1 Secret detail
// works the same way.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/key_value_table.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

/// Allowed wizard types — superset of common secret kinds, mirrors the
/// backend's `validSecretTypes` map.
const List<String> kSecretTypes = [
  'Opaque',
  'kubernetes.io/tls',
  'kubernetes.io/basic-auth',
  'kubernetes.io/dockerconfigjson',
];

class SecretForm {
  const SecretForm({
    this.name = '',
    this.namespace = '',
    this.type = 'Opaque',
    this.data = const <KeyValuePair>[],
  });

  final String name;
  final String namespace;
  final String type;
  final List<KeyValuePair> data;

  SecretForm copyWith({
    String? name,
    String? namespace,
    String? type,
    List<KeyValuePair>? data,
  }) =>
      SecretForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        type: type ?? this.type,
        data: data ?? this.data,
      );

  Map<String, String> dataAsMap() {
    final out = <String, String>{};
    for (final p in data) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }
}

class SecretWizardController extends WizardController<SecretForm> {
  @override
  String get wizardType => 'secret';

  @override
  String get resourceListKind => 'secrets';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Name, namespace, type, and data',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  SecretForm buildInitialForm() => const SecretForm();

  @override
  Map<String, dynamic> toPreviewBody(SecretForm form) {
    return {
      'name': form.name,
      'namespace': form.namespace,
      'type': form.type,
      'data': form.dataAsMap(),
    };
  }

  /// Single Configure step. Known paths: `name`, `namespace`, `type`,
  /// `data`, `data.<key>`, `data..<key>` (dockerconfigjson uses
  /// `data..dockerconfigjson` per `secret.go:Validate`). Unknown paths
  /// return null so the controller surfaces them via
  /// [WizardState.unrouted].
  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'type' ||
        fieldPath == 'data' ||
        fieldPath.startsWith('data.') ||
        fieldPath.startsWith('data[')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(SecretForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (!kSecretTypes.contains(form.type)) {
      out['type'] = 'Pick a supported secret type';
    }
    final filled = form.data.where((p) => p.key.isNotEmpty).toList();
    if (filled.isEmpty) {
      out['data'] = 'Add at least one key-value pair';
    }
    return out;
  }
}

final secretWizardProvider = AutoDisposeNotifierProvider.family<
    SecretWizardController,
    WizardState<SecretForm>,
    WizardKey>(SecretWizardController.new);
