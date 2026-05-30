// RestoreSnapshot wizard controller. Mirrors
// `frontend/islands/RestoreSnapshotWizard.tsx`.
//
// RestoreSnapshot is not its own backend wizard — it produces a PVC
// with a `dataSource` pointing at a VolumeSnapshot. Wire is `pvc`.
// The wizard's first step adds a snapshot picker to the standard
// PVC Configure form; the wire body merges in the dataSource.
//
// Step 0 — Snapshot + new PVC config (single screen)
// Step 1 — Review

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';
import '../pvc/pvc_wizard_controller.dart';

class RestoreSnapshotForm {
  const RestoreSnapshotForm({
    this.name = '',
    this.namespace = '',
    this.sourceSnapshot = '',
    this.storageClassName = '',
    this.sizeValue = '10',
    this.sizeUnit = 'Gi',
    this.accessMode = 'ReadWriteOnce',
  });

  /// Name of the *new* PVC.
  final String name;
  final String namespace;

  /// Name of the VolumeSnapshot to restore from. Lives in the same
  /// namespace as the new PVC (cross-namespace VolumeSnapshot
  /// references aren't supported by k8s).
  final String sourceSnapshot;

  final String storageClassName;
  final String sizeValue;
  final String sizeUnit;
  final String accessMode;

  RestoreSnapshotForm copyWith({
    String? name,
    String? namespace,
    String? sourceSnapshot,
    String? storageClassName,
    String? sizeValue,
    String? sizeUnit,
    String? accessMode,
  }) =>
      RestoreSnapshotForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        sourceSnapshot: sourceSnapshot ?? this.sourceSnapshot,
        storageClassName: storageClassName ?? this.storageClassName,
        sizeValue: sizeValue ?? this.sizeValue,
        sizeUnit: sizeUnit ?? this.sizeUnit,
        accessMode: accessMode ?? this.accessMode,
      );

  String get sizeQuantity => '$sizeValue$sizeUnit';
}

class RestoreSnapshotWizardController
    extends WizardController<RestoreSnapshotForm> {
  /// Backend wizard type is `pvc` — RestoreSnapshot is a UX wrapper
  /// that pre-populates `dataSource` and lands a PVC.
  @override
  String get wizardType => 'pvc';

  @override
  // Backend registry slug is 'pvcs' (not the k8s plural
  // 'persistentvolumeclaims') — must match the ResourceListKey slot the
  // PVC list/detail screens use so post-apply invalidation hits it.
  String get resourceListKind => 'pvcs';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Source & PVC',
          description: 'Snapshot, name, size, access mode',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  RestoreSnapshotForm buildInitialForm() => const RestoreSnapshotForm();

  @override
  Map<String, dynamic> toPreviewBody(RestoreSnapshotForm form) {
    return {
      'name': form.name,
      'namespace': form.namespace,
      'storageClassName': form.storageClassName,
      'size': form.sizeQuantity,
      'accessMode': form.accessMode,
      // PvcDataSource defaults kind/apiGroup to the VolumeSnapshot
      // values — only the snapshot name needs threading.
      'dataSource': PvcDataSource(name: form.sourceSnapshot).toJson(),
    };
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'storageClassName' ||
        fieldPath == 'size' ||
        fieldPath == 'accessMode' ||
        fieldPath.startsWith('dataSource.')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(
      RestoreSnapshotForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
    if (form.sourceSnapshot.trim().isEmpty) {
      out['dataSource.name'] = 'Pick a source snapshot';
    }
    if (form.storageClassName.trim().isEmpty) {
      out['storageClassName'] = 'Storage class is required';
    }
    final sz = double.tryParse(form.sizeValue.trim());
    if (sz == null || sz <= 0) {
      out['size'] = 'Size must be a positive number';
    }
    return out;
  }
}

final restoreSnapshotWizardProvider = AutoDisposeNotifierProvider.family<
    RestoreSnapshotWizardController,
    WizardState<RestoreSnapshotForm>,
    WizardKey>(RestoreSnapshotWizardController.new);
