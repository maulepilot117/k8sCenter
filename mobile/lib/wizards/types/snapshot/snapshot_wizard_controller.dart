// VolumeSnapshot wizard controller. Mirrors
// `frontend/islands/SnapshotWizard.tsx` and ports
// `backend/internal/wizard/snapshot.go:9`.
//
// Wire format:
//   { name, namespace, sourcePVC, volumeSnapshotClassName? }

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';

class SnapshotForm {
  const SnapshotForm({
    this.name = '',
    this.namespace = '',
    this.sourcePVC = '',
    this.volumeSnapshotClassName = '',
  });

  final String name;
  final String namespace;
  final String sourcePVC;
  final String volumeSnapshotClassName;

  SnapshotForm copyWith({
    String? name,
    String? namespace,
    String? sourcePVC,
    String? volumeSnapshotClassName,
  }) =>
      SnapshotForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        sourcePVC: sourcePVC ?? this.sourcePVC,
        volumeSnapshotClassName:
            volumeSnapshotClassName ?? this.volumeSnapshotClassName,
      );
}

class SnapshotWizardController extends WizardController<SnapshotForm> {
  @override
  String get wizardType => 'snapshot';

  @override
  String get resourceListKind => 'volumesnapshots';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Source PVC and snapshot class',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  SnapshotForm buildInitialForm() => const SnapshotForm();

  @override
  Map<String, dynamic> toPreviewBody(SnapshotForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'sourcePVC': form.sourcePVC,
    };
    final cls = form.volumeSnapshotClassName.trim();
    if (cls.isNotEmpty) body['volumeSnapshotClassName'] = cls;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'sourcePVC' ||
        fieldPath == 'volumeSnapshotClassName') {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(SnapshotForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
    if (form.sourcePVC.trim().isEmpty) {
      out['sourcePVC'] = 'Source PVC is required';
    }
    return out;
  }
}

final snapshotWizardProvider = AutoDisposeNotifierProvider.family<
    SnapshotWizardController,
    WizardState<SnapshotForm>,
    WizardKey>(SnapshotWizardController.new);
