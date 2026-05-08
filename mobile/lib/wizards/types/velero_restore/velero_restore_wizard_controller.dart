// Velero Restore wizard controller. Mirrors
// `frontend/islands/VeleroRestoreWizard.tsx` and
// `backend/internal/wizard/velero.go:103`.
//
// Wire format:
//   {
//     name, namespace?,           // backend defaults to `velero`
//     backupName?,                // one-of with scheduleName (mobile
//     scheduleName?,              //   surfaces backupName only)
//     includedNamespaces?,
//     excludedNamespaces?,
//     namespaceMapping?: { src -> dst },
//     restorePVs?: bool,
//   }

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/key_value_table.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

class VeleroRestoreForm {
  const VeleroRestoreForm({
    this.name = '',
    this.namespace = 'velero',
    this.backupName = '',
    this.namespaceMapping = const <KeyValuePair>[],
    this.restorePVs = true,
  });

  final String name;
  final String namespace;
  final String backupName;

  /// Source namespace -> target namespace. Each row is a KV pair so
  /// the same widget that powers ConfigMap/Secret data can drive it.
  final List<KeyValuePair> namespaceMapping;

  final bool restorePVs;

  VeleroRestoreForm copyWith({
    String? name,
    String? namespace,
    String? backupName,
    List<KeyValuePair>? namespaceMapping,
    bool? restorePVs,
  }) =>
      VeleroRestoreForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        backupName: backupName ?? this.backupName,
        namespaceMapping: namespaceMapping ?? this.namespaceMapping,
        restorePVs: restorePVs ?? this.restorePVs,
      );

  Map<String, String> mappingAsMap() {
    final out = <String, String>{};
    for (final p in namespaceMapping) {
      if (p.key.isEmpty || p.value.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }
}

class VeleroRestoreWizardController
    extends WizardController<VeleroRestoreForm> {
  @override
  String get wizardType => 'velero-restore';

  @override
  String get resourceListKind => 'restores';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Backup and namespace mapping',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  VeleroRestoreForm buildInitialForm() => const VeleroRestoreForm();

  @override
  Map<String, dynamic> toPreviewBody(VeleroRestoreForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'backupName': form.backupName,
      'restorePVs': form.restorePVs,
    };
    final mapping = form.mappingAsMap();
    if (mapping.isNotEmpty) body['namespaceMapping'] = mapping;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'backupName' ||
        fieldPath == 'scheduleName' ||
        fieldPath == 'namespaceMapping' ||
        fieldPath == 'restorePVs') {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(VeleroRestoreForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
    if (form.backupName.trim().isEmpty) {
      out['backupName'] = 'Pick a backup to restore from';
    }
    return out;
  }
}

final veleroRestoreWizardProvider = AutoDisposeNotifierProvider.family<
    VeleroRestoreWizardController,
    WizardState<VeleroRestoreForm>,
    WizardKey>(VeleroRestoreWizardController.new);
