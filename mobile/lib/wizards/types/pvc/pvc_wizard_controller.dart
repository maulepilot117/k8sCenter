// PVC wizard controller. Mirrors `frontend/islands/PVCWizard.tsx`
// and ports the wire contract from `backend/internal/wizard/pvc.go:11`.
//
// Wire format:
//   {
//     name, namespace,
//     storageClassName,
//     size,            // e.g. "10Gi" — value + unit concatenated
//     accessMode,      // single string, NOT an array
//     dataSource?: { name, kind, apiGroup }  // RestoreSnapshot path
//   }
//
// Note: backend takes a single accessMode string, not an array. The
// plan's "multi-checkbox" hint pre-dated reading the backend contract;
// R10 (web/Dart isomorphism) follows the actual wire shape.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<String> kAccessModes = [
  'ReadWriteOnce',
  'ReadOnlyMany',
  'ReadWriteMany',
  'ReadWriteOncePod',
];

const List<String> kSizeUnits = ['Mi', 'Gi', 'Ti'];

/// Optional dataSource — populated by RestoreSnapshot wizard, null
/// for the plain PVC wizard.
class PvcDataSource {
  const PvcDataSource({
    required this.name,
    this.kind = 'VolumeSnapshot',
    this.apiGroup = 'snapshot.storage.k8s.io',
  });

  final String name;
  final String kind;
  final String apiGroup;

  Map<String, String> toJson() => {
        'name': name,
        'kind': kind,
        'apiGroup': apiGroup,
      };
}

class PvcForm {
  const PvcForm({
    this.name = '',
    this.namespace = '',
    this.storageClassName = '',
    this.sizeValue = '10',
    this.sizeUnit = 'Gi',
    this.accessMode = 'ReadWriteOnce',
    this.dataSource,
  });

  final String name;
  final String namespace;
  final String storageClassName;
  final String sizeValue;
  final String sizeUnit;
  final String accessMode;
  final PvcDataSource? dataSource;

  PvcForm copyWith({
    String? name,
    String? namespace,
    String? storageClassName,
    String? sizeValue,
    String? sizeUnit,
    String? accessMode,
    PvcDataSource? dataSource,
    bool clearDataSource = false,
  }) =>
      PvcForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        storageClassName: storageClassName ?? this.storageClassName,
        sizeValue: sizeValue ?? this.sizeValue,
        sizeUnit: sizeUnit ?? this.sizeUnit,
        accessMode: accessMode ?? this.accessMode,
        dataSource: clearDataSource ? null : (dataSource ?? this.dataSource),
      );

  String get sizeQuantity => '$sizeValue$sizeUnit';
}

class PvcWizardController extends WizardController<PvcForm> {
  @override
  String get wizardType => 'pvc';

  @override
  String get resourceListKind => 'persistentvolumeclaims';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Storage class, size, access mode',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  PvcForm buildInitialForm() => const PvcForm();

  @override
  Map<String, dynamic> toPreviewBody(PvcForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'storageClassName': form.storageClassName,
      'size': form.sizeQuantity,
      'accessMode': form.accessMode,
    };
    if (form.dataSource != null) {
      body['dataSource'] = form.dataSource!.toJson();
    }
    return body;
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
  StepFieldErrors validateLocally(PvcForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
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

final pvcWizardProvider = AutoDisposeNotifierProvider.family<
    PvcWizardController, WizardState<PvcForm>, WizardKey>(
  PvcWizardController.new,
);
