// Job wizard screen — single Configure step + shared Review body.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../widgets/container_form_parts.dart';
import '../../widgets/optional_int_field.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'job_wizard_controller.dart';

class JobWizardScreen extends ConsumerStatefulWidget {
  const JobWizardScreen({super.key});

  @override
  ConsumerState<JobWizardScreen> createState() => _JobWizardScreenState();
}

class _JobWizardScreenState extends ConsumerState<JobWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<JobForm>(
      wizardType: 'job',
      title: 'New Job',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: jobWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<JobForm>(
              wizardKey: _wizardKey,
              controllerProvider: jobWizardProvider,
            ),
      ],
    );
  }
}

class _ConfigureStep extends ConsumerWidget {
  const _ConfigureStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(jobWizardProvider(wizardKey));
    final controller = ref.read(jobWizardProvider(wizardKey).notifier);
    final errors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'my-job',
            border: const OutlineInputBorder(),
            errorText: errors['name'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(name: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.namespace,
          decoration: InputDecoration(
            labelText: 'Namespace',
            hintText: 'default',
            border: const OutlineInputBorder(),
            errorText: errors['namespace'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.image,
          decoration: InputDecoration(
            labelText: 'Image',
            hintText: 'busybox:latest',
            border: const OutlineInputBorder(),
            errorText: errors['container.image'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(image: v)),
        ),
        const SizedBox(height: 16),
        DropdownButtonFormField<String>(
          initialValue: state.form.restartPolicy,
          items: const [
            DropdownMenuItem(value: 'Never', child: Text('Never')),
            DropdownMenuItem(value: 'OnFailure', child: Text('OnFailure')),
          ],
          onChanged: (v) {
            if (v == null) return;
            controller.updateForm((f) => f.copyWith(restartPolicy: v));
          },
          decoration: InputDecoration(
            labelText: 'Restart policy',
            border: const OutlineInputBorder(),
            errorText: errors['restartPolicy'],
          ),
        ),
        const SizedBox(height: 16),
        Row(
          children: [
            Expanded(
              child: OptionalIntField(
                label: 'Parallelism',
                hint: '1',
                value: state.form.parallelism,
                error: errors['parallelism'],
                onChanged: (n) => controller
                    .updateForm((f) => f.copyWith(parallelism: n)),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: OptionalIntField(
                label: 'Completions',
                hint: '1',
                value: state.form.completions,
                error: errors['completions'],
                onChanged: (n) => controller
                    .updateForm((f) => f.copyWith(completions: n)),
              ),
            ),
          ],
        ),
        const SizedBox(height: 16),
        OptionalIntField(
          label: 'Backoff limit',
          hint: '6',
          value: state.form.backoffLimit,
          error: errors['backoffLimit'],
          onChanged: (n) =>
              controller.updateForm((f) => f.copyWith(backoffLimit: n)),
        ),
        const SizedBox(height: 24),
        EnvVarSection(
          items: state.form.envVars,
          onChanged: (list) =>
              controller.updateForm((f) => f.copyWith(envVars: list)),
        ),
      ],
    );
  }
}

