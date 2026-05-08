// DaemonSet wizard controller. Mirrors `frontend/islands/DaemonSetWizard.tsx`.
//
// 2 steps: Configure (container basics + nodeSelector + probes) → Review.
//
// Wire format (`backend/internal/wizard/daemonset.go:14`):
//   {
//     name, namespace,
//     container: { image, envVars?, probes?, ... },
//     nodeSelector?, maxUnavailable?
//   }
//
// Daemons run one pod per node so there are no replicas. Probes live
// in the container sub-object, with field paths like
// `container.probes.liveness.port`.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/container_form_parts.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/probe_form.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

class DaemonSetForm {
  const DaemonSetForm({
    this.name = '',
    this.namespace = '',
    this.image = '',
    this.envVars = const <EnvVarData>[],
    this.nodeSelector = const <KeyValuePair>[],
    this.maxUnavailable = '',
    this.liveness,
    this.readiness,
  });

  final String name;
  final String namespace;
  final String image;
  final List<EnvVarData> envVars;
  final List<KeyValuePair> nodeSelector;
  final String maxUnavailable;
  final ProbeData? liveness;
  final ProbeData? readiness;

  DaemonSetForm copyWith({
    String? name,
    String? namespace,
    String? image,
    List<EnvVarData>? envVars,
    List<KeyValuePair>? nodeSelector,
    String? maxUnavailable,
    Object? liveness = _unset,
    Object? readiness = _unset,
  }) =>
      DaemonSetForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        image: image ?? this.image,
        envVars: envVars ?? this.envVars,
        nodeSelector: nodeSelector ?? this.nodeSelector,
        maxUnavailable: maxUnavailable ?? this.maxUnavailable,
        liveness: identical(liveness, _unset)
            ? this.liveness
            : liveness as ProbeData?,
        readiness: identical(readiness, _unset)
            ? this.readiness
            : readiness as ProbeData?,
      );

  Map<String, String> nodeSelectorAsMap() {
    final out = <String, String>{};
    for (final p in nodeSelector) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }
}

const Object _unset = Object();

class DaemonSetWizardController extends WizardController<DaemonSetForm> {
  @override
  String get wizardType => 'daemonset';

  @override
  String get resourceListKind => 'daemonsets';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
            title: 'Configure',
            description: 'Image, node selector, probes'),
        WizardStep(
            title: 'Review', description: 'Preview YAML and apply'),
      ];

  @override
  DaemonSetForm buildInitialForm() => const DaemonSetForm();

  @override
  Map<String, dynamic> toPreviewBody(DaemonSetForm form) {
    final container = <String, dynamic>{'image': form.image};
    final ev = envVarsAsJson(form.envVars);
    if (ev.isNotEmpty) container['envVars'] = ev;
    if (form.liveness != null || form.readiness != null) {
      final probes = <String, dynamic>{};
      if (form.liveness != null) probes['liveness'] = form.liveness!.toJson();
      if (form.readiness != null) {
        probes['readiness'] = form.readiness!.toJson();
      }
      container['probes'] = probes;
    }

    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'container': container,
    };
    final nodeSel = form.nodeSelectorAsMap();
    if (nodeSel.isNotEmpty) body['nodeSelector'] = nodeSel;
    if (form.maxUnavailable.trim().isNotEmpty) {
      body['maxUnavailable'] = form.maxUnavailable.trim();
    }
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'nodeSelector' ||
        fieldPath == 'maxUnavailable' ||
        fieldPath.startsWith('container')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(DaemonSetForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (form.image.trim().isEmpty) {
      out['container.image'] = 'Image is required';
    }
    return out;
  }
}

final daemonSetWizardProvider = AutoDisposeNotifierProvider.family<
    DaemonSetWizardController,
    WizardState<DaemonSetForm>,
    WizardKey>(DaemonSetWizardController.new);
