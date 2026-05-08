// Job wizard controller. Mirrors `frontend/islands/JobWizard.tsx`.
//
// 2 steps: Configure (image + restartPolicy + parallelism/completions/
// backoffLimit + env vars) → Review.
//
// Wire format (`backend/internal/wizard/job.go:11`):
//   {
//     name, namespace,
//     container: { image, envVars?, ... },
//     restartPolicy: "Never" | "OnFailure",
//     completions?, parallelism?, backoffLimit?, activeDeadlineSeconds?
//   }
//
// All container errors surface with `container.` prefix (see
// `ContainerInput.ValidateContainer` in `container.go:80`). Top-level
// field paths: `name`, `namespace`, `restartPolicy`, `completions`,
// `parallelism`, `backoffLimit`, `activeDeadlineSeconds`.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/container_form_parts.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<String> kJobRestartPolicies = ['Never', 'OnFailure'];

class JobForm {
  const JobForm({
    this.name = '',
    this.namespace = '',
    this.image = '',
    this.envVars = const <EnvVarData>[],
    this.restartPolicy = 'Never',
    this.parallelism,
    this.completions,
    this.backoffLimit,
  });

  final String name;
  final String namespace;
  final String image;
  final List<EnvVarData> envVars;
  final String restartPolicy;
  final int? parallelism;
  final int? completions;
  final int? backoffLimit;

  JobForm copyWith({
    String? name,
    String? namespace,
    String? image,
    List<EnvVarData>? envVars,
    String? restartPolicy,
    Object? parallelism = _unset,
    Object? completions = _unset,
    Object? backoffLimit = _unset,
  }) =>
      JobForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        image: image ?? this.image,
        envVars: envVars ?? this.envVars,
        restartPolicy: restartPolicy ?? this.restartPolicy,
        parallelism: identical(parallelism, _unset)
            ? this.parallelism
            : parallelism as int?,
        completions: identical(completions, _unset)
            ? this.completions
            : completions as int?,
        backoffLimit: identical(backoffLimit, _unset)
            ? this.backoffLimit
            : backoffLimit as int?,
      );
}

const Object _unset = Object();

/// Build the `container` JSON sub-object from the form's image + env
/// vars. Reused by CronJob's controller for the embedded job template
/// — keeps the two wizards' container-shape serialization identical.
Map<String, dynamic> buildJobContainerJson({
  required String image,
  required List<EnvVarData> envVars,
}) {
  final out = <String, dynamic>{'image': image};
  final ev = envVarsAsJson(envVars);
  if (ev.isNotEmpty) out['envVars'] = ev;
  return out;
}

class JobWizardController extends WizardController<JobForm> {
  @override
  String get wizardType => 'job';

  @override
  String get resourceListKind => 'jobs';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
            title: 'Configure',
            description: 'Image, restart policy, parallelism, env'),
        WizardStep(
            title: 'Review', description: 'Preview YAML and apply'),
      ];

  @override
  JobForm buildInitialForm() => const JobForm();

  @override
  Map<String, dynamic> toPreviewBody(JobForm form) {
    final body = <String, dynamic>{
      'name': form.name,
      'namespace': form.namespace,
      'container':
          buildJobContainerJson(image: form.image, envVars: form.envVars),
      'restartPolicy': form.restartPolicy,
    };
    if (form.parallelism != null) body['parallelism'] = form.parallelism;
    if (form.completions != null) body['completions'] = form.completions;
    if (form.backoffLimit != null) body['backoffLimit'] = form.backoffLimit;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'restartPolicy' ||
        fieldPath == 'parallelism' ||
        fieldPath == 'completions' ||
        fieldPath == 'backoffLimit' ||
        fieldPath == 'activeDeadlineSeconds' ||
        fieldPath.startsWith('container')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(JobForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (form.image.trim().isEmpty) {
      out['container.image'] = 'Image is required';
    }
    if (!kJobRestartPolicies.contains(form.restartPolicy)) {
      out['restartPolicy'] = 'Pick a restart policy';
    }
    if (form.parallelism != null && form.parallelism! < 0) {
      out['parallelism'] = 'Must be 0 or greater';
    }
    if (form.completions != null && form.completions! < 0) {
      out['completions'] = 'Must be 0 or greater';
    }
    if (form.backoffLimit != null && form.backoffLimit! < 0) {
      out['backoffLimit'] = 'Must be 0 or greater';
    }
    return out;
  }
}

final jobWizardProvider = AutoDisposeNotifierProvider.family<
    JobWizardController, WizardState<JobForm>, WizardKey>(
    JobWizardController.new);
