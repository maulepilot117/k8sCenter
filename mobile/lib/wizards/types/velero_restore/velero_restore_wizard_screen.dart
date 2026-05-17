// Velero Restore wizard screen. Single Configure step (name + Velero
// namespace + backup picker + namespace mapping + restorePVs toggle)
// → Review.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../widgets/key_value_table.dart';
import '../../../widgets/named_resource_picker.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'velero_restore_wizard_controller.dart';

class VeleroRestoreWizardScreen extends ConsumerStatefulWidget {
  const VeleroRestoreWizardScreen({super.key});

  @override
  ConsumerState<VeleroRestoreWizardScreen> createState() =>
      _VeleroRestoreWizardScreenState();
}

class _VeleroRestoreWizardScreenState
    extends ConsumerState<VeleroRestoreWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<VeleroRestoreForm>(
      wizardType: 'velero-restore',
      title: 'New Velero Restore',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: veleroRestoreWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<VeleroRestoreForm>(
              wizardKey: _wizardKey,
              controllerProvider: veleroRestoreWizardProvider,
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
    final state = ref.watch(veleroRestoreWizardProvider(wizardKey));
    final controller =
        ref.read(veleroRestoreWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Restore name',
            hintText: 'production-restore-2026-05-08',
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
        // Velero Backup CRDs live in the Velero namespace; use the
        // form's namespace as the picker scope so swapping clusters
        // (which changes the Velero namespace) keeps the list correct.
        NamedResourcePicker(
          clusterId: wizardKey.clusterId,
          kind: 'backups',
          namespace: state.form.namespace.isEmpty ? null : state.form.namespace,
          selected: state.form.backupName,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(backupName: v)),
          label: 'Source backup',
          hint: 'Pick a Velero Backup',
          errorMessage: stepErrors['backupName'],
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader(
          'Namespace mapping (optional)',
          subtitle: 'Restore namespace src → namespace dst',
        ),
        const SizedBox(height: 8),
        KeyValueTable(
          pairs: state.form.namespaceMapping,
          onChanged: (kv) => controller
              .updateForm((f) => f.copyWith(namespaceMapping: kv)),
          keyLabel: 'Source namespace',
          valueLabel: 'Target namespace',
          errorMessage: stepErrors['namespaceMapping'],
        ),
        const SizedBox(height: 16),
        SwitchListTile(
          title: const Text('Restore persistent volumes'),
          subtitle: const Text(
            'When enabled, Velero re-creates the PVs from snapshots. '
            'Disable to restore only Kubernetes objects.',
          ),
          value: state.form.restorePVs,
          contentPadding: EdgeInsets.zero,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(restorePVs: v)),
        ),
      ],
    );
  }
}
