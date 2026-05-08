// Velero Schedule wizard screen. 3 steps:
//   0 — Schedule (cron + pause)
//   1 — Backup template (mirrors VeleroBackup's Configure step)
//   2 — Review

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../widgets/duration_input.dart';
import '../../widgets/multi_namespace_picker.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'velero_schedule_wizard_controller.dart';

class VeleroScheduleWizardScreen extends ConsumerStatefulWidget {
  const VeleroScheduleWizardScreen({super.key});

  @override
  ConsumerState<VeleroScheduleWizardScreen> createState() =>
      _VeleroScheduleWizardScreenState();
}

class _VeleroScheduleWizardScreenState
    extends ConsumerState<VeleroScheduleWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<VeleroScheduleForm>(
      wizardType: 'velero-schedule',
      title: 'New Velero Schedule',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: veleroScheduleWizardProvider,
      stepBuilders: [
        (ctx) => _ScheduleStep(wizardKey: _wizardKey),
        (ctx) => _TemplateStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<VeleroScheduleForm>(
              wizardKey: _wizardKey,
              controllerProvider: veleroScheduleWizardProvider,
            ),
      ],
    );
  }
}

class _ScheduleStep extends ConsumerWidget {
  const _ScheduleStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(veleroScheduleWizardProvider(wizardKey));
    final controller =
        ref.read(veleroScheduleWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Schedule name',
            hintText: 'production-nightly',
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
            labelText: 'Velero namespace',
            hintText: 'velero',
            border: const OutlineInputBorder(),
            errorText: stepErrors['namespace'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v)),
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader('Cron schedule'),
        const SizedBox(height: 8),
        TextFormField(
          initialValue: state.form.schedule,
          decoration: InputDecoration(
            labelText: 'Cron',
            hintText: '0 1 * * *',
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
            for (final preset in kVeleroCronPresets)
              ActionChip(
                label: Text(preset.label),
                onPressed: () => controller
                    .updateForm((f) => f.copyWith(schedule: preset.value)),
              ),
          ],
        ),
        const SizedBox(height: 8),
        SwitchListTile(
          title: const Text('Paused'),
          subtitle: const Text(
              'Schedule is created but does not fire until unpaused'),
          value: state.form.paused,
          contentPadding: EdgeInsets.zero,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(paused: v)),
        ),
      ],
    );
  }
}

class _TemplateStep extends ConsumerWidget {
  const _TemplateStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(veleroScheduleWizardProvider(wizardKey));
    final controller =
        ref.read(veleroScheduleWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[1] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        MultiNamespacePicker(
          clusterId: wizardKey.clusterId,
          selected: state.form.includedNamespaces,
          onChanged: (s) => controller
              .updateForm((f) => f.copyWith(includedNamespaces: s)),
          label: 'Included namespaces',
          helperText: 'Empty includes everything.',
          errorMessage: stepErrors['includedNamespaces'],
          disabledNamespaces: state.form.excludedNamespaces,
        ),
        const SizedBox(height: 16),
        MultiNamespacePicker(
          clusterId: wizardKey.clusterId,
          selected: state.form.excludedNamespaces,
          onChanged: (s) => controller
              .updateForm((f) => f.copyWith(excludedNamespaces: s)),
          label: 'Excluded namespaces',
          errorMessage: stepErrors['excludedNamespaces'],
          disabledNamespaces: state.form.includedNamespaces,
        ),
        const SizedBox(height: 16),
        DurationInput(
          label: 'TTL (optional)',
          value: state.form.ttl,
          onChanged: (v) => controller.updateForm((f) => f.copyWith(ttl: v)),
          errorText: stepErrors['ttl'],
          helperText: 'How long Velero keeps each backup this schedule '
              'creates. Default: 30 days.',
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.storageLocation,
          decoration: InputDecoration(
            labelText: 'Storage location (optional)',
            hintText: 'default',
            border: const OutlineInputBorder(),
            errorText: stepErrors['storageLocation'],
          ),
          onChanged: (v) => controller
              .updateForm((f) => f.copyWith(storageLocation: v.trim())),
        ),
        const SizedBox(height: 8),
        SwitchListTile(
          title: const Text('Snapshot volumes'),
          subtitle: const Text(
              'Capture PV snapshots via the configured snapshotter'),
          value: state.form.snapshotVolumes,
          contentPadding: EdgeInsets.zero,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(snapshotVolumes: v)),
        ),
      ],
    );
  }
}
