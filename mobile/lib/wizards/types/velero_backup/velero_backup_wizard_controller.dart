// Velero Backup wizard controller. Mirrors
// `frontend/islands/VeleroBackupWizard.tsx` and ports
// `backend/internal/wizard/velero.go:10`.
//
// Wire format:
//   {
//     name, namespace?,           // backend defaults to `velero`
//     includedNamespaces?: [],
//     excludedNamespaces?: [],
//     storageLocation?: string,
//     ttl?: string,               // Velero duration (e.g. "168h")
//     snapshotVolumes?: bool,
//     labels?: Map<String,String>,
//   }
//
// Backend tolerates an empty top-level Namespace and substitutes
// `velero`, but the mobile wizard always sends an explicit value so
// the operator's intent is unambiguous in the YAML preview.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/duration_input.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

class VeleroBackupForm {
  const VeleroBackupForm({
    this.name = '',
    this.namespace = 'velero',
    this.includedNamespaces = const <String>{},
    this.excludedNamespaces = const <String>{},
    this.includeClusterResources = false,
    this.storageLocation = '',
    this.ttl = '',
    this.snapshotVolumes = true,
  });

  final String name;
  final String namespace;
  final Set<String> includedNamespaces;
  final Set<String> excludedNamespaces;
  final bool includeClusterResources;
  final String storageLocation;

  /// Velero duration string. Empty → omit the field (Velero default).
  final String ttl;

  final bool snapshotVolumes;

  VeleroBackupForm copyWith({
    String? name,
    String? namespace,
    Set<String>? includedNamespaces,
    Set<String>? excludedNamespaces,
    bool? includeClusterResources,
    String? storageLocation,
    String? ttl,
    bool? snapshotVolumes,
  }) =>
      VeleroBackupForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        includedNamespaces: includedNamespaces ?? this.includedNamespaces,
        excludedNamespaces: excludedNamespaces ?? this.excludedNamespaces,
        includeClusterResources:
            includeClusterResources ?? this.includeClusterResources,
        storageLocation: storageLocation ?? this.storageLocation,
        ttl: ttl ?? this.ttl,
        snapshotVolumes: snapshotVolumes ?? this.snapshotVolumes,
      );
}

class VeleroBackupWizardController
    extends WizardController<VeleroBackupForm> {
  @override
  String get wizardType => 'velero-backup';

  @override
  String get resourceListKind => 'backups';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Scope, storage, retention',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  VeleroBackupForm buildInitialForm() => const VeleroBackupForm();

  @override
  Map<String, dynamic> toPreviewBody(VeleroBackupForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
    };
    if (form.includedNamespaces.isNotEmpty) {
      body['includedNamespaces'] = form.includedNamespaces.toList()..sort();
    }
    if (form.excludedNamespaces.isNotEmpty) {
      body['excludedNamespaces'] = form.excludedNamespaces.toList()..sort();
    }
    if (form.storageLocation.trim().isNotEmpty) {
      body['storageLocation'] = form.storageLocation.trim();
    }
    if (form.ttl.trim().isNotEmpty) {
      body['ttl'] = form.ttl.trim();
    }
    // Always emit snapshotVolumes — toggling it explicitly in the form
    // is the operator's intent. Backend treats `null` as default-true,
    // but sending the bool keeps the YAML preview deterministic.
    body['snapshotVolumes'] = form.snapshotVolumes;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'includedNamespaces' ||
        fieldPath == 'excludedNamespaces' ||
        fieldPath == 'storageLocation' ||
        fieldPath == 'ttl' ||
        fieldPath == 'snapshotVolumes') {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(VeleroBackupForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
    final overlap = form.includedNamespaces
        .intersection(form.excludedNamespaces);
    if (overlap.isNotEmpty) {
      out['includedNamespaces'] =
          'Namespace cannot appear in both Included and Excluded: '
          '${overlap.join(", ")}';
    }
    final ttlErr = validateDuration(form.ttl);
    if (ttlErr != null) out['ttl'] = ttlErr;
    return out;
  }
}

final veleroBackupWizardProvider = AutoDisposeNotifierProvider.family<
    VeleroBackupWizardController,
    WizardState<VeleroBackupForm>,
    WizardKey>(VeleroBackupWizardController.new);
