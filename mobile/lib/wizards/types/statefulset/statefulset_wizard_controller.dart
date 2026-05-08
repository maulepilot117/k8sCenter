// StatefulSet wizard controller. Mirrors `frontend/islands/StatefulSetWizard.tsx`.
//
// 2 steps: Configure (image + serviceName + replicas + volumeClaim
// templates) → Review.
//
// Wire format (`backend/internal/wizard/statefulset.go:23`):
//   {
//     name, namespace, serviceName, replicas: int,
//     container: { image, envVars?, ... },
//     volumeClaimTemplates?: [{name, storageClassName, size, accessMode}],
//     podManagementPolicy?
//   }
//
// VCT field paths look like `volumeClaimTemplates[0].size` — routed
// to the Configure step (the only form step).

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/container_form_parts.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<String> kStsAccessModes = [
  'ReadWriteOnce',
  'ReadWriteMany',
  'ReadOnlyMany',
  'ReadWriteOncePod',
];

const List<String> kStsPodManagementPolicies = ['OrderedReady', 'Parallel'];

class VolumeClaimTemplate {
  const VolumeClaimTemplate({
    this.name = '',
    this.storageClassName = '',
    this.size = '',
    this.accessMode = 'ReadWriteOnce',
  });

  final String name;
  final String storageClassName;
  final String size;
  final String accessMode;

  VolumeClaimTemplate copyWith({
    String? name,
    String? storageClassName,
    String? size,
    String? accessMode,
  }) =>
      VolumeClaimTemplate(
        name: name ?? this.name,
        storageClassName: storageClassName ?? this.storageClassName,
        size: size ?? this.size,
        accessMode: accessMode ?? this.accessMode,
      );

  Map<String, dynamic> toJson() => {
        'name': name,
        'storageClassName': storageClassName,
        'size': size,
        'accessMode': accessMode,
      };

  bool get isEmpty => name.isEmpty && size.isEmpty;
}

class StatefulSetForm {
  const StatefulSetForm({
    this.name = '',
    this.namespace = '',
    this.serviceName = '',
    this.replicas = 1,
    this.image = '',
    this.envVars = const <EnvVarData>[],
    this.volumeClaimTemplates = const <VolumeClaimTemplate>[],
    this.podManagementPolicy = 'OrderedReady',
  });

  final String name;
  final String namespace;
  final String serviceName;
  final int replicas;
  final String image;
  final List<EnvVarData> envVars;
  final List<VolumeClaimTemplate> volumeClaimTemplates;
  final String podManagementPolicy;

  StatefulSetForm copyWith({
    String? name,
    String? namespace,
    String? serviceName,
    int? replicas,
    String? image,
    List<EnvVarData>? envVars,
    List<VolumeClaimTemplate>? volumeClaimTemplates,
    String? podManagementPolicy,
  }) =>
      StatefulSetForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        serviceName: serviceName ?? this.serviceName,
        replicas: replicas ?? this.replicas,
        image: image ?? this.image,
        envVars: envVars ?? this.envVars,
        volumeClaimTemplates:
            volumeClaimTemplates ?? this.volumeClaimTemplates,
        podManagementPolicy: podManagementPolicy ?? this.podManagementPolicy,
      );

  List<Map<String, dynamic>> volumeClaimTemplatesJson() {
    return [
      for (final v in volumeClaimTemplates)
        if (!v.isEmpty) v.toJson(),
    ];
  }
}

class StatefulSetWizardController
    extends WizardController<StatefulSetForm> {
  @override
  String get wizardType => 'statefulset';

  @override
  String get resourceListKind => 'statefulsets';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
            title: 'Configure',
            description:
                'Service, replicas, image, volume claim templates'),
        WizardStep(
            title: 'Review', description: 'Preview YAML and apply'),
      ];

  @override
  StatefulSetForm buildInitialForm() => const StatefulSetForm();

  @override
  Map<String, dynamic> toPreviewBody(StatefulSetForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'serviceName': form.serviceName,
      'replicas': form.replicas,
      'container':
          buildContainerJson(image: form.image, envVars: form.envVars),
      'podManagementPolicy': form.podManagementPolicy,
    };
    final vcts = form.volumeClaimTemplatesJson();
    if (vcts.isNotEmpty) body['volumeClaimTemplates'] = vcts;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'serviceName' ||
        fieldPath == 'replicas' ||
        fieldPath == 'podManagementPolicy' ||
        fieldPath.startsWith('container') ||
        fieldPath.startsWith('volumeClaimTemplates')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(StatefulSetForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (form.serviceName.trim().isEmpty) {
      out['serviceName'] = 'Service name is required';
    }
    if (form.image.trim().isEmpty) {
      out['container.image'] = 'Image is required';
    }
    if (form.replicas < 0 || form.replicas > 1000) {
      out['replicas'] = 'Replicas must be between 0 and 1000';
    }
    return out;
  }
}

final statefulSetWizardProvider = AutoDisposeNotifierProvider.family<
    StatefulSetWizardController,
    WizardState<StatefulSetForm>,
    WizardKey>(StatefulSetWizardController.new);
