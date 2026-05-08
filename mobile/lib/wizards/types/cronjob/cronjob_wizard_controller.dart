// CronJob wizard controller. Mirrors `frontend/islands/CronJobWizard.tsx`.
//
// 2 steps: Configure (schedule + concurrency + suspend + embedded job
// template) → Review.
//
// Wire format (`backend/internal/wizard/cronjob.go:14`):
//   {
//     name, namespace, schedule,
//     container: { image, envVars?, ... },
//     restartPolicy: "Never" | "OnFailure",
//     concurrencyPolicy: "Allow" | "Forbid" | "Replace",
//     successfulJobsHistoryLimit?, failedJobsHistoryLimit?, suspend?
//   }
//
// The embedded job template (image + restartPolicy + envVars) reuses
// the same `buildJobContainerJson` helper Job's controller exposes,
// keeping the two wizards' container shapes structurally identical.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../widgets/container_form_parts.dart';
import '../../wizard_controller.dart';
import '../../wizard_step.dart';

const List<String> kCronConcurrencyPolicies = ['Allow', 'Forbid', 'Replace'];

/// Hand-curated common patterns for the schedule picker. Plan calls
/// these out as the same set the web frontend offers — keeps mobile
/// muscle memory aligned with desktop.
const List<String> kCronCommonPatterns = [
  '@hourly',
  '@daily',
  '@weekly',
  '@monthly',
  '0 */6 * * *',
  '*/15 * * * *',
];

class CronJobForm {
  const CronJobForm({
    this.name = '',
    this.namespace = '',
    this.schedule = '',
    this.image = '',
    this.envVars = const <EnvVarData>[],
    this.restartPolicy = 'OnFailure',
    this.concurrencyPolicy = 'Allow',
    this.suspend = false,
  });

  final String name;
  final String namespace;
  final String schedule;
  final String image;
  final List<EnvVarData> envVars;
  final String restartPolicy;
  final String concurrencyPolicy;
  final bool suspend;

  CronJobForm copyWith({
    String? name,
    String? namespace,
    String? schedule,
    String? image,
    List<EnvVarData>? envVars,
    String? restartPolicy,
    String? concurrencyPolicy,
    bool? suspend,
  }) =>
      CronJobForm(
        name: name ?? this.name,
        namespace: namespace ?? this.namespace,
        schedule: schedule ?? this.schedule,
        image: image ?? this.image,
        envVars: envVars ?? this.envVars,
        restartPolicy: restartPolicy ?? this.restartPolicy,
        concurrencyPolicy: concurrencyPolicy ?? this.concurrencyPolicy,
        suspend: suspend ?? this.suspend,
      );
}

class CronJobWizardController extends WizardController<CronJobForm> {
  @override
  String get wizardType => 'cronjob';

  @override
  String get resourceListKind => 'cronjobs';

  @override
  List<WizardStep> get steps => const [
        WizardStep(
            title: 'Configure',
            description: 'Schedule, concurrency, container'),
        WizardStep(
            title: 'Review', description: 'Preview YAML and apply'),
      ];

  @override
  CronJobForm buildInitialForm() => const CronJobForm();

  @override
  Map<String, dynamic> toPreviewBody(CronJobForm form) {
    return {
      'name': form.name,
      'namespace': form.namespace,
      'schedule': form.schedule,
      'container':
          buildContainerJson(image: form.image, envVars: form.envVars),
      'restartPolicy': form.restartPolicy,
      'concurrencyPolicy': form.concurrencyPolicy,
      if (form.suspend) 'suspend': true,
    };
  }

  @override
  int? errorRouter(String fieldPath) {
    // History-limit fields aren't surfaced as form inputs, so a server
    // error against them has no place to render. Let them fall through
    // to state.unrouted so the operator sees the raw message instead
    // of a silent stepErrors[0] swallow.
    if (fieldPath == 'name' ||
        fieldPath == 'namespace' ||
        fieldPath == 'schedule' ||
        fieldPath == 'restartPolicy' ||
        fieldPath == 'concurrencyPolicy' ||
        fieldPath == 'suspend' ||
        fieldPath.startsWith('container')) {
      return 0;
    }
    return null;
  }

  @override
  StepFieldErrors validateLocally(CronJobForm form, int stepIndex) {
    if (stepIndex != 0) return const <String, String>{};
    final out = <String, String>{};
    if (form.name.trim().isEmpty) out['name'] = 'Name is required';
    if (form.namespace.trim().isEmpty) {
      out['namespace'] = 'Namespace is required';
    }
    if (form.schedule.trim().isEmpty) {
      out['schedule'] = 'Schedule is required';
    }
    if (form.image.trim().isEmpty) {
      out['container.image'] = 'Image is required';
    }
    return out;
  }
}

final cronJobWizardProvider = AutoDisposeNotifierProvider.family<
    CronJobWizardController, WizardState<CronJobForm>, WizardKey>(
    CronJobWizardController.new);
