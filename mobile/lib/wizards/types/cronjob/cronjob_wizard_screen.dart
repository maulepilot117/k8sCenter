// CronJob wizard screen — single Configure step + shared Review body.
// The schedule input pairs a free-text field with a "common patterns"
// chip row; tapping a chip overwrites the field.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/container_form_parts.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'cronjob_wizard_controller.dart';

class CronJobWizardScreen extends ConsumerStatefulWidget {
  const CronJobWizardScreen({super.key});

  @override
  ConsumerState<CronJobWizardScreen> createState() =>
      _CronJobWizardScreenState();
}

class _CronJobWizardScreenState extends ConsumerState<CronJobWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<CronJobForm>(
      wizardType: 'cronjob',
      title: 'New CronJob',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: cronJobWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<CronJobForm>(
              wizardKey: _wizardKey,
              controllerProvider: cronJobWizardProvider,
            ),
      ],
    );
  }
}

class _ConfigureStep extends ConsumerStatefulWidget {
  const _ConfigureStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  ConsumerState<_ConfigureStep> createState() => _ConfigureStepState();
}

class _ConfigureStepState extends ConsumerState<_ConfigureStep> {
  /// Owned controller so chip taps can update the schedule field's
  /// visible text (overwrites whatever the operator typed).
  final TextEditingController _scheduleCtl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _scheduleCtl.text =
        ref.read(cronJobWizardProvider(widget.wizardKey)).form.schedule;
  }

  @override
  void dispose() {
    _scheduleCtl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(cronJobWizardProvider(widget.wizardKey));
    final controller =
        ref.read(cronJobWizardProvider(widget.wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final errors = state.stepErrors[0] ?? const <String, String>{};

    // Sync external state edits (e.g., discardAndReset) into the
    // controller. didUpdateWidget would also work but the ConsumerState
    // pattern doesn't fire there for ref watches.
    if (_scheduleCtl.text != state.form.schedule) {
      _scheduleCtl.text = state.form.schedule;
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'nightly-cleanup',
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
        TextField(
          controller: _scheduleCtl,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(schedule: v)),
          decoration: InputDecoration(
            labelText: 'Schedule',
            hintText: '0 2 * * *',
            border: const OutlineInputBorder(),
            errorText: errors['schedule'],
            helperText:
                'Standard 5-field cron (minute hour day month weekday)',
          ),
        ),
        const SizedBox(height: 8),
        Wrap(
          spacing: 6,
          runSpacing: 4,
          children: [
            for (final pattern in kCronCommonPatterns)
              ActionChip(
                label: Text(pattern),
                onPressed: () {
                  _scheduleCtl.text = pattern;
                  controller
                      .updateForm((f) => f.copyWith(schedule: pattern));
                },
              ),
          ],
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
        Row(
          children: [
            Expanded(
              child: DropdownButtonFormField<String>(
                initialValue: state.form.restartPolicy,
                items: const [
                  DropdownMenuItem(
                      value: 'OnFailure', child: Text('OnFailure')),
                  DropdownMenuItem(value: 'Never', child: Text('Never')),
                ],
                onChanged: (v) {
                  if (v == null) return;
                  controller.updateForm(
                      (f) => f.copyWith(restartPolicy: v));
                },
                decoration: InputDecoration(
                  labelText: 'Restart policy',
                  border: const OutlineInputBorder(),
                  errorText: errors['restartPolicy'],
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: DropdownButtonFormField<String>(
                initialValue: state.form.concurrencyPolicy,
                items: const [
                  DropdownMenuItem(value: 'Allow', child: Text('Allow')),
                  DropdownMenuItem(value: 'Forbid', child: Text('Forbid')),
                  DropdownMenuItem(value: 'Replace', child: Text('Replace')),
                ],
                onChanged: (v) {
                  if (v == null) return;
                  controller.updateForm(
                      (f) => f.copyWith(concurrencyPolicy: v));
                },
                decoration: InputDecoration(
                  labelText: 'Concurrency',
                  border: const OutlineInputBorder(),
                  errorText: errors['concurrencyPolicy'],
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 16),
        SwitchListTile(
          title: const Text('Suspend'),
          subtitle: Text(
            'Pause scheduling until the operator unsuspends. New jobs '
            'are not created while suspended.',
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
          value: state.form.suspend,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(suspend: v)),
          contentPadding: EdgeInsets.zero,
        ),
        const SizedBox(height: 16),
        Text(
          'Environment variables',
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 12),
        RepeatingRowGroup<EnvVarData>(
          items: state.form.envVars,
          itemBuilder: (ctx, i, item) => EnvVarRow(
            value: item,
            onChanged: (next) {
              final list = [...state.form.envVars];
              list[i] = next;
              controller.updateForm((f) => f.copyWith(envVars: list));
            },
          ),
          onAdd: () => controller.updateForm((f) => f.copyWith(
                envVars: [...f.envVars, const EnvVarData()],
              )),
          onRemove: (i) {
            final list = [...state.form.envVars]..removeAt(i);
            controller.updateForm((f) => f.copyWith(envVars: list));
          },
          addLabel: 'Add env var',
          emptyMessage: 'No env vars defined.',
        ),
      ],
    );
  }
}
