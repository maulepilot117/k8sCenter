// Deployment wizard controller. Mirrors `frontend/islands/DeploymentWizard.tsx`.
//
// 4 steps: Basics → Networking → Resources → Review. Form state is
// flat (not nested under a Container record) because the backend's
// `DeploymentInput` itself flattens image/ports/envVars/resources/
// probes onto the deployment input (see `backend/internal/wizard/
// deployment.go:14`). Field paths in 422 responses are therefore flat
// too — `image`, `ports[0].containerPort`, `resources.requestCpu`,
// `probes.liveness.path`, etc.
//
// Wire format:
//   {
//     name, namespace, image, replicas: int,
//     labels?, ports?, envVars?, resources?, probes?, strategy?
//   }

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/container_form_parts.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/probe_form.dart';
import '../../widgets/resources_form.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

class DeploymentForm {
  const DeploymentForm({
    this.name = '',
    this.namespace = '',
    this.image = '',
    this.replicas = 1,
    this.labels = const <KeyValuePair>[],
    this.envVars = const <EnvVarData>[],
    this.ports = const <ContainerPortData>[],
    this.resources = const ResourcesData(),
    this.liveness,
    this.readiness,
  });

  final String name;
  final String namespace;
  final String image;
  final int replicas;
  final List<KeyValuePair> labels;
  final List<EnvVarData> envVars;
  final List<ContainerPortData> ports;
  final ResourcesData resources;
  final ProbeData? liveness;
  final ProbeData? readiness;

  DeploymentForm copyWith({
    String? name,
    String? namespace,
    String? image,
    int? replicas,
    List<KeyValuePair>? labels,
    List<EnvVarData>? envVars,
    List<ContainerPortData>? ports,
    ResourcesData? resources,
    Object? liveness = kFormFieldUnset,
    Object? readiness = kFormFieldUnset,
  }) =>
      DeploymentForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        image: image ?? this.image,
        replicas: replicas ?? this.replicas,
        labels: labels ?? this.labels,
        envVars: envVars ?? this.envVars,
        ports: ports ?? this.ports,
        resources: resources ?? this.resources,
        liveness: identical(liveness, kFormFieldUnset)
            ? this.liveness
            : liveness as ProbeData?,
        readiness: identical(readiness, kFormFieldUnset)
            ? this.readiness
            : readiness as ProbeData?,
      );

  Map<String, String> labelsAsMap() {
    final out = <String, String>{};
    for (final p in labels) {
      if (p.key.isEmpty) continue;
      out[p.key] = p.value;
    }
    return out;
  }
}

class DeploymentWizardController extends WizardController<DeploymentForm> {
  @override
  String get wizardType => 'deployment';

  @override
  String get resourceListKind => 'deployments';

  @override
  List<WizardStep> get steps => const [
        WizardStep(title: 'Basics', description: 'Name, image, replicas'),
        WizardStep(title: 'Networking', description: 'Container ports'),
        WizardStep(title: 'Resources', description: 'Resources + probes'),
        WizardStep(title: 'Review', description: 'Preview YAML and apply'),
      ];

  @override
  DeploymentForm buildInitialForm() => const DeploymentForm();

  @override
  Map<String, dynamic> toPreviewBody(DeploymentForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'image': form.image,
      'replicas': form.replicas,
    };
    final labels = form.labelsAsMap();
    if (labels.isNotEmpty) body['labels'] = labels;

    final ports = containerPortsAsJson(form.ports);
    if (ports.isNotEmpty) body['ports'] = ports;

    final envVars = envVarsAsJson(form.envVars);
    if (envVars.isNotEmpty) body['envVars'] = envVars;

    final resources = form.resources.toJson();
    if (resources != null) body['resources'] = resources;

    if (form.liveness != null || form.readiness != null) {
      final probes = <String, dynamic>{};
      if (form.liveness != null) probes['liveness'] = form.liveness!.toJson();
      if (form.readiness != null) {
        probes['readiness'] = form.readiness!.toJson();
      }
      body['probes'] = probes;
    }
    return body;
  }

  /// Maps backend field paths to step indices:
  ///   * Basics (0): name, namespace, image, replicas, labels, envVars,
  ///     probes.* — probes don't have their own step on Deployment in
  ///     the plan; they share the Resources step. Routed there.
  ///   * Networking (1): ports.*
  ///   * Resources (2): resources.*, probes.*
  ///   * Review (3) is read-only.
  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath.startsWith('ports')) return 1;
    if (fieldPath.startsWith('resources') || fieldPath.startsWith('probes')) {
      return 2;
    }
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'image' ||
        fieldPath == 'replicas' ||
        fieldPath == 'labels' ||
        fieldPath.startsWith('labels[') ||
        fieldPath == 'envVars' ||
        fieldPath.startsWith('envVars[') ||
        fieldPath.startsWith('strategy')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(DeploymentForm form, int stepIndex) {
    if (stepIndex == 0) {
      final out = <String, String>{};
      if (form.name.trim().isEmpty) out['name'] = 'Name is required';
      if (form.namespace.trim().isEmpty) {
        out['namespace'] = 'Namespace is required';
      }
      if (form.image.trim().isEmpty) out['image'] = 'Image is required';
      if (form.replicas < 0 || form.replicas > 1000) {
        out['replicas'] = 'Replicas must be between 0 and 1000';
      }
      return out;
    }
    return const <String, String>{};
  }
}

final deploymentWizardProvider = AutoDisposeNotifierProvider.family<
    DeploymentWizardController,
    WizardState<DeploymentForm>,
    WizardKey>(DeploymentWizardController.new);
