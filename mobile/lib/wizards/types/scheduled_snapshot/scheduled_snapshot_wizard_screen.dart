// ScheduledSnapshot wizard screen. 3-step:
//   0 — Source & Schedule
//   1 — Class & Retention
//   2 — Review
//
// Apply lands a 4-doc multi-resource YAML (ServiceAccount, Role,
// RoleBinding, CronJob) — the controller's existing partial-apply
// gate (summary.failed > 0 → failed) handles per-doc failures.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/named_resource_picker.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'scheduled_snapshot_wizard_controller.dart';

class ScheduledSnapshotWizardScreen extends ConsumerStatefulWidget {
  const ScheduledSnapshotWizardScreen({super.key});

  @override
  ConsumerState<ScheduledSnapshotWizardScreen> createState() =>
      _ScheduledSnapshotWizardScreenState();
}

class _ScheduledSnapshotWizardScreenState
    extends ConsumerState<ScheduledSnapshotWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<ScheduledSnapshotForm>(
      wizardType: 'scheduled-snapshot',
      title: 'New scheduled snapshot',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: scheduledSnapshotWizardProvider,
      stepBuilders: [
        (ctx) => _SourceStep(wizardKey: _wizardKey),
        (ctx) => _RetentionStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<ScheduledSnapshotForm>(
              wizardKey: _wizardKey,
              controllerProvider: scheduledSnapshotWizardProvider,
            ),
      ],
    );
  }
}

class _SourceStep extends ConsumerWidget {
  const _SourceStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(scheduledSnapshotWizardProvider(wizardKey));
    final controller =
        ref.read(scheduledSnapshotWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'nightly-data-snapshot',
            border: const OutlineInputBorder(),
            errorText: stepErrors['name'],
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
            errorText: stepErrors['namespace'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v)),
        ),
        const SizedBox(height: 16),
        NamedResourcePicker(
          clusterId: wizardKey.clusterId,
          kind: 'persistentvolumeclaims',
          namespace:
              state.form.namespace.isEmpty ? null : state.form.namespace,
          selected: state.form.sourcePVC,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(sourcePVC: v)),
          label: 'Source PVC',
          errorMessage: stepErrors['sourcePVC'],
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader('Schedule (cron expression)'),
        const SizedBox(height: 8),
        TextFormField(
          initialValue: state.form.schedule,
          decoration: InputDecoration(
            labelText: 'Cron',
            hintText: '0 2 * * *',
            border: const OutlineInputBorder(),
            errorText: stepErrors['schedule'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(schedule: v.trim())),
        ),
        const SizedBox(height: 8),
        Wrap(
          spacing: 8,
          runSpacing: 4,
          children: [
            for (final preset in kCronPresets)
              ActionChip(
                label: Text(preset.label),
                onPressed: () => controller
                    .updateForm((f) => f.copyWith(schedule: preset.value)),
              ),
          ],
        ),
      ],
    );
  }
}

class _RetentionStep extends ConsumerWidget {
  const _RetentionStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(scheduledSnapshotWizardProvider(wizardKey));
    final controller =
        ref.read(scheduledSnapshotWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[1] ?? const <String, String>{};
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.volumeSnapshotClassName,
          decoration: InputDecoration(
            labelText: 'VolumeSnapshotClass',
            hintText: 'csi-hostpath-snapclass',
            border: const OutlineInputBorder(),
            errorText: stepErrors['volumeSnapshotClassName'],
          ),
          onChanged: (v) => controller.updateForm(
              (f) => f.copyWith(volumeSnapshotClassName: v.trim())),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.retentionCount.toString(),
          keyboardType: TextInputType.number,
          decoration: InputDecoration(
            labelText: 'Retention (snapshots to keep)',
            hintText: '7',
            helperText: 'Older snapshots are deleted (1 – 100)',
            border: const OutlineInputBorder(),
            errorText: stepErrors['retentionCount'],
          ),
          onChanged: (v) {
            final parsed = int.tryParse(v.trim()) ?? 0;
            controller
                .updateForm((f) => f.copyWith(retentionCount: parsed));
          },
        ),
        const SizedBox(height: 8),
        Text(
          'A CronJob runs `kubectl apply` for a new VolumeSnapshot on the '
          'configured schedule, then prunes the oldest snapshots beyond '
          'the retention count.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
      ],
    );
  }
}
