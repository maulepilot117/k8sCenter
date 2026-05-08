// NamespaceLimits wizard controller. Mirrors
// `frontend/islands/NamespaceLimitsWizard.tsx` and ports the wire
// contract from `backend/internal/wizard/namespace_limits.go:53`.
//
// Multi-resource: backend emits a single YAML preview with two
// documents (`---`-separated) — a ResourceQuota and a LimitRange —
// and the apply endpoint reports per-document results.
//
// Wire format (`NamespaceLimitsInput`, required-only subset for v1):
//   {
//     namespace, quotaName, limitRangeName,
//     quota:  {cpuHard, memoryHard, podsHard},
//     limits: {
//       containerDefault:        {cpu, memory},
//       containerDefaultRequest: {cpu, memory},
//       containerMax:            {cpu, memory},
//       containerMin:            {cpu, memory},
//     },
//   }
//
// Optional fields (secretsHard, gpuHard, podMax, pvc storage limits,
// thresholds) are deferred to a follow-up — operators that want them
// today have the YAML editor.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../wizard_controller.dart';
import '../../wizard_step.dart';

class ResourcePair {
  const ResourcePair({this.cpu = '', this.memory = ''});

  final String cpu;
  final String memory;

  ResourcePair copyWith({String? cpu, String? memory}) =>
      ResourcePair(cpu: cpu ?? this.cpu, memory: memory ?? this.memory);

  Map<String, String> toJson() => {'cpu': cpu, 'memory': memory};
}

class NamespaceLimitsForm {
  const NamespaceLimitsForm({
    this.namespace = '',
    this.quotaName = '',
    this.limitRangeName = '',
    this.cpuHard = '',
    this.memoryHard = '',
    this.podsHard = 0,
    this.containerDefault = const ResourcePair(),
    this.containerDefaultRequest = const ResourcePair(),
    this.containerMax = const ResourcePair(),
    this.containerMin = const ResourcePair(),
  });

  final String namespace;
  final String quotaName;
  final String limitRangeName;
  final String cpuHard;
  final String memoryHard;
  final int podsHard;
  final ResourcePair containerDefault;
  final ResourcePair containerDefaultRequest;
  final ResourcePair containerMax;
  final ResourcePair containerMin;

  NamespaceLimitsForm copyWith({
    String? namespace,
    String? quotaName,
    String? limitRangeName,
    String? cpuHard,
    String? memoryHard,
    int? podsHard,
    ResourcePair? containerDefault,
    ResourcePair? containerDefaultRequest,
    ResourcePair? containerMax,
    ResourcePair? containerMin,
  }) =>
      NamespaceLimitsForm(
        namespace: namespace ?? this.namespace,
        quotaName: quotaName ?? this.quotaName,
        limitRangeName: limitRangeName ?? this.limitRangeName,
        cpuHard: cpuHard ?? this.cpuHard,
        memoryHard: memoryHard ?? this.memoryHard,
        podsHard: podsHard ?? this.podsHard,
        containerDefault: containerDefault ?? this.containerDefault,
        containerDefaultRequest:
            containerDefaultRequest ?? this.containerDefaultRequest,
        containerMax: containerMax ?? this.containerMax,
        containerMin: containerMin ?? this.containerMin,
      );
}

class NamespaceLimitsWizardController
    extends WizardController<NamespaceLimitsForm> {
  @override
  String get wizardType => 'namespace-limits';

  /// The wizard creates two resources — a ResourceQuota and a
  /// LimitRange. We invalidate the quotas list since that's what the
  /// dashboard's namespace-limits view reads first; LimitRange list
  /// will refetch on next visit.
  @override
  String get resourceListKind => 'resourcequotas';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
          title: 'Configure',
          description: 'Quota + LimitRange — applied as one operation',
        ),
        WizardStep(
          title: 'Review',
          description: 'Preview multi-doc YAML and apply',
        ),
      ];

  @override
  NamespaceLimitsForm buildInitialForm() => const NamespaceLimitsForm();

  @override
  Map<String, dynamic> toPreviewBody(NamespaceLimitsForm form) {
    return <String, dynamic>{
      'namespace': form.namespace,
      'quotaName': form.quotaName,
      'limitRangeName': form.limitRangeName,
      'quota': {
        'cpuHard': form.cpuHard,
        'memoryHard': form.memoryHard,
        'podsHard': form.podsHard,
      },
      'limits': {
        'containerDefault': form.containerDefault.toJson(),
        'containerDefaultRequest': form.containerDefaultRequest.toJson(),
        'containerMax': form.containerMax.toJson(),
        'containerMin': form.containerMin.toJson(),
      },
    };
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'namespace' ||
        fieldPath == 'quotaName' ||
        fieldPath == 'limitRangeName' ||
        fieldPath.startsWith('quota.') ||
        fieldPath.startsWith('limits.')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(
      NamespaceLimitsForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (form.quotaName.trim().isEmpty) {
      out['quotaName'] = 'Quota name is required';
    }
    if (form.limitRangeName.trim().isEmpty) {
      out['limitRangeName'] = 'LimitRange name is required';
    }
    if (form.cpuHard.trim().isEmpty) {
      out['quota.cpuHard'] = 'CPU quota is required';
    }
    if (form.memoryHard.trim().isEmpty) {
      out['quota.memoryHard'] = 'Memory quota is required';
    }
    if (form.podsHard < 1) {
      out['quota.podsHard'] = 'Pod quota must be at least 1';
    }
    void requirePair(String prefix, ResourcePair p) {
      if (p.cpu.trim().isEmpty) {
        out['$prefix.cpu'] = 'CPU is required';
      }
      if (p.memory.trim().isEmpty) {
        out['$prefix.memory'] = 'Memory is required';
      }
    }

    requirePair('limits.containerDefault', form.containerDefault);
    requirePair(
        'limits.containerDefaultRequest', form.containerDefaultRequest);
    requirePair('limits.containerMax', form.containerMax);
    requirePair('limits.containerMin', form.containerMin);
    return out;
  }
}

final namespaceLimitsWizardProvider = AutoDisposeNotifierProvider.family<
    NamespaceLimitsWizardController,
    WizardState<NamespaceLimitsForm>,
    WizardKey>(NamespaceLimitsWizardController.new);
