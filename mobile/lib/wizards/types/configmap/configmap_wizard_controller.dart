// ConfigMap wizard controller. Mirrors `frontend/islands/ConfigMapWizard.tsx`.
//
// 2 steps: Configure (name + namespace + key-value rows) → Review (YAML
// preview + apply). Server-side validation handles all field rules
// (DNS-1123, key regex, max 1MB total) — local validation only checks
// "name + namespace not empty" + "data has at least one row" so the
// operator hits the server only when the form is plausibly complete.
//
// Wire format (`backend/internal/wizard/configmap.go:13`):
//   { name: string, namespace: string, data: Map<String, String> }

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/key_value_table.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

class ConfigMapForm {
  const ConfigMapForm({
    this.name = '',
    this.namespace = '',
    this.data = const <KeyValuePair>[],
  });

  final String name;
  final String namespace;
  final List<KeyValuePair> data;

  ConfigMapForm copyWith({
    String? name,
    String? namespace,
    List<KeyValuePair>? data,
  }) =>
      ConfigMapForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        data: data ?? this.data,
      );

  /// Strip empty rows (any row with empty key) before serializing —
  /// they're sentinels in the editor, not actual data.
  Map<String, String> dataAsMap() {
    final out = <String, String>{};
    for (final p in data) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }
}

class ConfigMapWizardController
    extends WizardController<ConfigMapForm> {
  @override
  String get wizardType => 'configmap';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Name, namespace, and key-value data',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  /// Initial form is empty. M3 doesn't pre-fill namespace from a
  /// "last-used" memory — the operator types it. Web wizard defaults
  /// to `default` only when explicitly passed; mobile follows the same
  /// "operator chooses" stance.
  @override
  ConfigMapForm buildInitialForm() => const ConfigMapForm();

  @override
  Map<String, dynamic> toPreviewBody(ConfigMapForm form) {
    return {
      'name': form.name,
      'namespace': form.namespace,
      'data': form.dataAsMap(),
    };
  }

  /// Field paths from `configmap.go:Validate` map to step 0 (Configure)
  /// — that's the only form step. Review owns the YAML preview only.
  @override
  int errorRouter(String fieldPath) => 0;

  @override
  StepFieldErrors validateLocally(ConfigMapForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) {
      out['name'] = 'Name is required';
    }
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    final filled = form.data.where((p) => p.key.isNotEmpty).toList();
    if (filled.isEmpty) {
      out['data'] = 'Add at least one key-value pair';
    }
    return out;
  }
}

final configMapWizardProvider = AutoDisposeNotifierProvider.family<
    ConfigMapWizardController,
    WizardState<ConfigMapForm>,
    WizardKey>(ConfigMapWizardController.new);
