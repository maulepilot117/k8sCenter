// RestoreSnapshot wizard screen. Step 0 lets the operator pick an
// existing VolumeSnapshot in their namespace and configure the new
// PVC that will be bound to it. Step 1 is the standard Review.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../widgets/list_picker_screen.dart';
import '../../widgets/named_resource_picker.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import '../pvc/pvc_wizard_controller.dart' show kAccessModes, kSizeUnits;
import 'restore_snapshot_wizard_controller.dart';

class RestoreSnapshotWizardScreen extends ConsumerStatefulWidget {
  const RestoreSnapshotWizardScreen({super.key});

  @override
  ConsumerState<RestoreSnapshotWizardScreen> createState() =>
      _RestoreSnapshotWizardScreenState();
}

class _RestoreSnapshotWizardScreenState
    extends ConsumerState<RestoreSnapshotWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<RestoreSnapshotForm>(
      wizardType: 'pvc',
      title: 'Restore from snapshot',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: restoreSnapshotWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<RestoreSnapshotForm>(
              wizardKey: _wizardKey,
              controllerProvider: restoreSnapshotWizardProvider,
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
    final state = ref.watch(restoreSnapshotWizardProvider(wizardKey));
    final controller =
        ref.read(restoreSnapshotWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.namespace,
          decoration: InputDecoration(
            labelText: 'Namespace',
            hintText: 'default',
            border: const OutlineInputBorder(),
            errorText: stepErrors['namespace'],
            helperText:
                'New PVC and source snapshot must share a namespace',
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v)),
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader('Source snapshot'),
        const SizedBox(height: 8),
        ListPickerScreen(
          clusterId: wizardKey.clusterId,
          kind: 'volumesnapshots',
          namespace: state.form.namespace.isEmpty ? null : state.form.namespace,
          selectedName: state.form.sourceSnapshot,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(sourceSnapshot: v)),
          subtitleBuilder: (item) {
            final status = item['status'] as Map?;
            final restoreSize = status?['restoreSize'] as String?;
            final ts = (item['metadata'] as Map?)?['creationTimestamp']
                as String?;
            final parts = <String>[
              ?restoreSize,
              ?ts,
            ];
            return parts.isEmpty ? null : parts.join(' · ');
          },
          emptyTitle: 'No snapshots in this namespace',
          emptyMessage: 'Create one with the Snapshot wizard, then return.',
          errorMessage: stepErrors['dataSource.name'],
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader('New PVC'),
        const SizedBox(height: 8),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'New PVC name',
            hintText: 'data-restored',
            border: const OutlineInputBorder(),
            errorText: stepErrors['name'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(name: v)),
        ),
        const SizedBox(height: 16),
        NamedResourcePicker(
          clusterId: wizardKey.clusterId,
          kind: 'storageclasses',
          namespace: null,
          selected: state.form.storageClassName,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(storageClassName: v)),
          label: 'Storage class',
          errorMessage: stepErrors['storageClassName'],
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader('Size'),
        const SizedBox(height: 8),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              flex: 3,
              child: TextFormField(
                initialValue: state.form.sizeValue,
                keyboardType:
                    const TextInputType.numberWithOptions(decimal: true),
                decoration: InputDecoration(
                  labelText: 'Value',
                  border: const OutlineInputBorder(),
                  errorText: stepErrors['size'],
                ),
                onChanged: (v) => controller
                    .updateForm((f) => f.copyWith(sizeValue: v.trim())),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              flex: 2,
              child: DropdownButtonFormField<String>(
                initialValue: state.form.sizeUnit,
                decoration: const InputDecoration(
                  labelText: 'Unit',
                  border: OutlineInputBorder(),
                ),
                items: [
                  for (final u in kSizeUnits)
                    DropdownMenuItem(value: u, child: Text(u)),
                ],
                onChanged: (v) {
                  if (v == null) return;
                  controller.updateForm((f) => f.copyWith(sizeUnit: v));
                },
              ),
            ),
          ],
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader('Access mode'),
        const SizedBox(height: 8),
        DropdownButtonFormField<String>(
          initialValue: state.form.accessMode,
          isExpanded: true,
          decoration: InputDecoration(
            border: const OutlineInputBorder(),
            errorText: stepErrors['accessMode'],
          ),
          items: [
            for (final mode in kAccessModes)
              DropdownMenuItem(value: mode, child: Text(mode)),
          ],
          onChanged: (v) {
            if (v == null) return;
            controller.updateForm((f) => f.copyWith(accessMode: v));
          },
        ),
      ],
    );
  }
}
