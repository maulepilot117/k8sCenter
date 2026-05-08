// StorageClass wizard controller. Mirrors
// `frontend/islands/StorageClassWizard.tsx` and ports the wire
// contract from `backend/internal/wizard/storage.go:29`.
//
// Cluster-scoped — no namespace field. Wire format:
//   {
//     name, provisioner,
//     reclaimPolicy?, volumeBindingMode?,
//     allowVolumeExpansion?, isDefault?,
//     parameters?:  Map<String,String>,
//     mountOptions?: List<String>,
//   }

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/key_value_table.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<String> kReclaimPolicies = ['Delete', 'Retain'];
const List<String> kVolumeBindingModes = [
  'Immediate',
  'WaitForFirstConsumer',
];

class StorageClassForm {
  const StorageClassForm({
    this.name = '',
    this.provisioner = '',
    this.reclaimPolicy = 'Delete',
    this.volumeBindingMode = 'Immediate',
    this.allowVolumeExpansion = false,
    this.isDefault = false,
    this.parameters = const <KeyValuePair>[],
    this.mountOptions = '',
  });

  final String name;
  final String provisioner;
  final String reclaimPolicy;
  final String volumeBindingMode;
  final bool allowVolumeExpansion;
  final bool isDefault;
  final List<KeyValuePair> parameters;

  /// Comma- or newline-separated raw text. Parsed into a list at
  /// `toPreviewBody` time.
  final String mountOptions;

  StorageClassForm copyWith({
    String? name,
    String? provisioner,
    String? reclaimPolicy,
    String? volumeBindingMode,
    bool? allowVolumeExpansion,
    bool? isDefault,
    List<KeyValuePair>? parameters,
    String? mountOptions,
  }) =>
      StorageClassForm(
        name: name ?? this.name,
        provisioner: provisioner ?? this.provisioner,
        reclaimPolicy: reclaimPolicy ?? this.reclaimPolicy,
        volumeBindingMode: volumeBindingMode ?? this.volumeBindingMode,
        allowVolumeExpansion:
            allowVolumeExpansion ?? this.allowVolumeExpansion,
        isDefault: isDefault ?? this.isDefault,
        parameters: parameters ?? this.parameters,
        mountOptions: mountOptions ?? this.mountOptions,
      );

  Map<String, String> parametersMap() {
    final out = <String, String>{};
    for (final p in parameters) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }

  List<String> mountOptionsList() {
    return mountOptions
        .split(RegExp(r'[,\n]'))
        .map((s) => s.trim())
        .where((s) => s.isNotEmpty)
        .toList();
  }
}

class StorageClassWizardController
    extends WizardController<StorageClassForm> {
  @override
  String get wizardType => 'storageclass';

  @override
  String get resourceListKind => 'storageclasses';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Provisioner, parameters, and policies',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  StorageClassForm buildInitialForm() => const StorageClassForm();

  @override
  Map<String, dynamic> toPreviewBody(StorageClassForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'provisioner': form.provisioner,
    };
    if (form.reclaimPolicy.isNotEmpty) {
      body['reclaimPolicy'] = form.reclaimPolicy;
    }
    if (form.volumeBindingMode.isNotEmpty) {
      body['volumeBindingMode'] = form.volumeBindingMode;
    }
    if (form.allowVolumeExpansion) {
      body['allowVolumeExpansion'] = true;
    }
    if (form.isDefault) {
      body['isDefault'] = true;
    }
    final params = form.parametersMap();
    if (params.isNotEmpty) body['parameters'] = params;
    final opts = form.mountOptionsList();
    if (opts.isNotEmpty) body['mountOptions'] = opts;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'provisioner' ||
        fieldPath == 'reclaimPolicy' ||
        fieldPath == 'volumeBindingMode' ||
        fieldPath == 'parameters' ||
        fieldPath == 'mountOptions') {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(StorageClassForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.provisioner.trim().isEmpty) {
      out['provisioner'] = 'Provisioner is required';
    }
    return out;
  }
}

final storageClassWizardProvider = AutoDisposeNotifierProvider.family<
    StorageClassWizardController,
    WizardState<StorageClassForm>,
    WizardKey>(StorageClassWizardController.new);
