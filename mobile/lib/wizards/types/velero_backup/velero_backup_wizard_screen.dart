// Velero Backup wizard screen. One Configure step (name + Velero
// namespace + included/excluded namespace pickers + TTL + storage
// location + snapshotVolumes toggle) → Review.

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
import 'velero_backup_wizard_controller.dart';

class VeleroBackupWizardScreen extends ConsumerStatefulWidget {
  const VeleroBackupWizardScreen({super.key});

  @override
  ConsumerState<VeleroBackupWizardScreen> createState() =>
      _VeleroBackupWizardScreenState();
}

class _VeleroBackupWizardScreenState
    extends ConsumerState<VeleroBackupWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<VeleroBackupForm>(
      wizardType: 'velero-backup',
      title: 'New Velero Backup',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: veleroBackupWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<VeleroBackupForm>(
              wizardKey: _wizardKey,
              controllerProvider: veleroBackupWizardProvider,
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
    final state = ref.watch(veleroBackupWizardProvider(wizardKey));
    final controller =
        ref.read(veleroBackupWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Backup name',
            hintText: 'production-2026-05-08',
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
            helperText: 'Where the Velero operator runs',
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v)),
        ),
        const SizedBox(height: 16),
        MultiNamespacePicker(
          clusterId: wizardKey.clusterId,
          selected: state.form.includedNamespaces,
          onChanged: (s) => controller
              .updateForm((f) => f.copyWith(includedNamespaces: s)),
          label: 'Included namespaces',
          helperText:
              'Empty includes everything. Adding entries restricts the '
              'backup to those namespaces.',
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
          helperText: 'Cannot overlap Included.',
          errorMessage: stepErrors['excludedNamespaces'],
          disabledNamespaces: state.form.includedNamespaces,
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader('Retention & storage'),
        const SizedBox(height: 8),
        DurationInput(
          label: 'TTL (optional)',
          value: state.form.ttl,
          onChanged: (v) => controller.updateForm((f) => f.copyWith(ttl: v)),
          errorText: stepErrors['ttl'],
          helperText: 'How long Velero keeps the backup. Default: 30 days.',
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.storageLocation,
          decoration: InputDecoration(
            labelText: 'Storage location (optional)',
            hintText: 'default',
            border: const OutlineInputBorder(),
            errorText: stepErrors['storageLocation'],
            helperText: 'Velero BackupStorageLocation name',
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
