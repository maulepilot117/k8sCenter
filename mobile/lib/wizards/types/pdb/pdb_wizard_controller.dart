// PDB wizard controller. Mirrors `frontend/islands/PDBWizard.tsx` and
// ports the wire contract from `backend/internal/wizard/pdb.go:18`.
//
// Wire format (`PDBInput`):
//   {
//     name, namespace,
//     selector: Map<String,String>,         // ≥1 label
//     // exactly one of:
//     minAvailable?:  string,               // "2" or "50%"
//     maxUnavailable?: string,
//   }
//
// One Configure step + Review. Mutual exclusion is enforced at the
// form layer — the radio picks which field to send, so the backend
// never sees both populated.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/key_value_table.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

/// Which field the wizard emits.
enum PdbPolicy { minAvailable, maxUnavailable }

class PdbForm {
  const PdbForm({
    this.name = '',
    this.namespace = '',
    this.selector = const <KeyValuePair>[],
    this.policy = PdbPolicy.minAvailable,
    this.value = '',
  });

  final String name;
  final String namespace;
  final List<KeyValuePair> selector;
  final PdbPolicy policy;

  /// String form so percentages ("50%") and integers ("2") share one
  /// input field — exactly the backend's wire shape.
  final String value;

  PdbForm copyWith({
    String? name,
    String? namespace,
    List<KeyValuePair>? selector,
    PdbPolicy? policy,
    String? value,
  }) =>
      PdbForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        selector: selector ?? this.selector,
        policy: policy ?? this.policy,
        value: value ?? this.value,
      );

  Map<String, String> selectorMap() {
    final out = <String, String>{};
    for (final p in selector) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }
}

class PdbWizardController extends WizardController<PdbForm> {
  @override
  String get wizardType => 'pdb';

  @override
  String get resourceListKind => 'poddisruptionbudgets';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Selector and disruption policy',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  PdbForm buildInitialForm() => const PdbForm();

  @override
  Map<String, dynamic> toPreviewBody(PdbForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'selector': form.selectorMap(),
    };
    if (form.policy == PdbPolicy.minAvailable) {
      body['minAvailable'] = form.value;
    } else {
      body['maxUnavailable'] = form.value;
    }
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'selector' ||
        fieldPath == 'minAvailable' ||
        fieldPath == 'maxUnavailable' ||
        fieldPath.startsWith('selector')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(PdbForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (form.selectorMap().isEmpty) {
      out['selector'] = 'At least one label selector is required';
    }
    if (form.value.trim().isEmpty) {
      final field = form.policy == PdbPolicy.minAvailable
          ? 'minAvailable'
          : 'maxUnavailable';
      out[field] = 'Value is required (e.g. 2 or 50%)';
    }
    return out;
  }
}

final pdbWizardProvider = AutoDisposeNotifierProvider.family<
    PdbWizardController, WizardState<PdbForm>, WizardKey>(
    PdbWizardController.new);
