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
    Object? parallelism = kFormFieldUnset,
    Object? completions = kFormFieldUnset,
    Object? backoffLimit = kFormFieldUnset,
  }) =>
      JobForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        image: image ?? this.image,
        envVars: envVars ?? this.envVars,
        restartPolicy: restartPolicy ?? this.restartPolicy,
        parallelism: identical(parallelism, kFormFieldUnset)
            ? this.parallelism
            : parallelism as int?,
        completions: identical(completions, kFormFieldUnset)
            ? this.completions
            : completions as int?,
        backoffLimit: identical(backoffLimit, kFormFieldUnset)
            ? this.backoffLimit
            : backoffLimit as int?,
      );
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
          buildContainerJson(image: form.image, envVars: form.envVars),
      'restartPolicy': form.restartPolicy,
    };
    if (form.parallelism != null) body['parallelism'] = form.parallelism;
    if (form.completions != null) body['completions'] = form.completions;
    if (form.backoffLimit != null) body['backoffLimit'] = form.backoffLimit;
    return body;
  }

  @override
  int? errorRouter(String fieldPath) {
    // `activeDeadlineSeconds` isn't surfaced as a form field, so a
    // server error against it has no input to render under. Let it
    // fall through to state.unrouted so the operator at least sees
    // the raw message instead of a silent stepErrors[0] swallow.
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'restartPolicy' ||
        fieldPath == 'parallelism' ||
        fieldPath == 'completions' ||
        fieldPath == 'backoffLimit' ||
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
