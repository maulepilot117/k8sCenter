// HPA wizard controller. Mirrors `frontend/islands/HPAWizard.tsx` and
// ports the wire contract from `backend/internal/wizard/hpa.go:15`.
//
// Wire format (`HPAInput`):
//   {
//     name, namespace,
//     targetKind:  "Deployment" | "StatefulSet" | "ReplicaSet",
//     targetName:  string,
//     minReplicas?: int,
//     maxReplicas:  int,
//     metrics: [{type: "Resource", resourceName: "cpu"|"memory",
//                targetType: "Utilization"|"AverageValue",
//                targetAverageValue: int}],
//   }
//
// One Configure step + Review.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<String> kHpaTargetKinds = ['Deployment', 'StatefulSet', 'ReplicaSet'];
const List<String> kHpaResourceNames = ['cpu', 'memory'];
const List<String> kHpaTargetTypes = ['Utilization', 'AverageValue'];

class HpaMetric {
  const HpaMetric({
    this.resourceName = 'cpu',
    this.targetType = 'Utilization',
    this.targetAverageValue = 80,
  });

  final String resourceName;
  final String targetType;
  final int targetAverageValue;

  HpaMetric copyWith({
    String? resourceName,
    String? targetType,
    int? targetAverageValue,
  }) =>
      HpaMetric(
        resourceName: resourceName ?? this.resourceName,
        targetType: targetType ?? this.targetType,
        targetAverageValue: targetAverageValue ?? this.targetAverageValue,
      );

  Map<String, dynamic> toJson() => {
        'type': 'Resource',
        'resourceName': resourceName,
        'targetType': targetType,
        'targetAverageValue': targetAverageValue,
      };
}

class HpaForm {
  const HpaForm({
    this.name = '',
    this.namespace = '',
    this.targetKind = 'Deployment',
    this.targetName = '',
    this.minReplicas,
    this.maxReplicas = 10,
    this.metrics = const <HpaMetric>[HpaMetric()],
  });

  final String name;
  final String namespace;
  final String targetKind;
  final String targetName;
  final int? minReplicas;
  final int maxReplicas;
  final List<HpaMetric> metrics;

  HpaForm copyWith({
    String? name,
    String? namespace,
    String? targetKind,
    String? targetName,
    int? minReplicas,
    bool clearMinReplicas = false,
    int? maxReplicas,
    List<HpaMetric>? metrics,
  }) =>
      HpaForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        targetKind: targetKind ?? this.targetKind,
        targetName: targetName ?? this.targetName,
        minReplicas:
            clearMinReplicas ? null : (minReplicas ?? this.minReplicas),
        maxReplicas: maxReplicas ?? this.maxReplicas,
        metrics: metrics ?? this.metrics,
      );
}

class HpaWizardController extends WizardController<HpaForm> {
  @override
  String get wizardType => 'hpa';

  @override
  String get resourceListKind => 'horizontalpodautoscalers';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Target, scaling bounds, and metrics',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview YAML and apply',
        ),
      ];

  @override
  HpaForm buildInitialForm() => const HpaForm();

  @override
  Map<String, dynamic> toPreviewBody(HpaForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'targetKind': form.targetKind,
      'targetName': form.targetName,
      'maxReplicas': form.maxReplicas,
      'metrics': [for (final m in form.metrics) m.toJson()],
    };
    if (form.minReplicas != null) {
      body['minReplicas'] = form.minReplicas;
    }
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'targetKind' ||
        fieldPath == 'targetName' ||
        fieldPath == 'minReplicas' ||
        fieldPath == 'maxReplicas' ||
        fieldPath == 'metrics' ||
        fieldPath.startsWith('metrics[')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(HpaForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{
      ...validateNameAndNamespace(form.name, form.namespace),
    };
    if (form.targetName.trim().isEmpty) {
      out['targetName'] = 'Target name is required';
    }
    if (form.maxReplicas < 1) {
      out['maxReplicas'] = 'maxReplicas must be at least 1';
    }
    if (form.minReplicas != null) {
      if (form.minReplicas! < 1) {
        out['minReplicas'] = 'minReplicas must be at least 1';
      } else if (form.minReplicas! > form.maxReplicas) {
        out['minReplicas'] = 'minReplicas must not exceed maxReplicas';
      }
    }
    if (form.metrics.isEmpty) {
      out['metrics'] = 'Add at least one metric';
    }
    return out;
  }
}

final hpaWizardProvider = AutoDisposeNotifierProvider.family<
    HpaWizardController, WizardState<HpaForm>, WizardKey>(
    HpaWizardController.new);
