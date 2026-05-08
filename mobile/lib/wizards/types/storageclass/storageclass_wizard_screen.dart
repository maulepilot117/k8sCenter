// StorageClass wizard screen. Cluster-scoped — no namespace input.
// Configure step composes name + provisioner + reclaim/binding-mode
// radios + allowExpansion + isDefault toggles + parameters table +
// mountOptions free-text.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'storageclass_wizard_controller.dart';

class StorageClassWizardScreen extends ConsumerStatefulWidget {
  const StorageClassWizardScreen({super.key});

  @override
  ConsumerState<StorageClassWizardScreen> createState() =>
      _StorageClassWizardScreenState();
}

class _StorageClassWizardScreenState
    extends ConsumerState<StorageClassWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<StorageClassForm>(
      wizardType: 'storageclass',
      title: 'New StorageClass',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: storageClassWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<StorageClassForm>(
              wizardKey: _wizardKey,
              controllerProvider: storageClassWizardProvider,
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
    final state = ref.watch(storageClassWizardProvider(wizardKey));
    final controller =
        ref.read(storageClassWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'fast-ssd',
            border: const OutlineInputBorder(),
            errorText: stepErrors['name'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(name: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.provisioner,
          decoration: InputDecoration(
            labelText: 'Provisioner',
            hintText: 'kubernetes.io/aws-ebs',
            border: const OutlineInputBorder(),
            errorText: stepErrors['provisioner'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(provisioner: v.trim())),
        ),
        const SizedBox(height: 24),
        const WizardSectionHeader('Reclaim policy'),
        const SizedBox(height: 8),
        SegmentedButton<String>(
          segments: const [
            ButtonSegment(value: 'Delete', label: Text('Delete')),
            ButtonSegment(value: 'Retain', label: Text('Retain')),
          ],
          selected: {state.form.reclaimPolicy},
          showSelectedIcon: false,
          onSelectionChanged: (s) =>
              controller.updateForm((f) => f.copyWith(reclaimPolicy: s.first)),
        ),
        if (stepErrors['reclaimPolicy'] != null)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Text(
              stepErrors['reclaimPolicy']!,
              style: TextStyle(color: colors.error, fontSize: 12),
            ),
          ),
        const SizedBox(height: 24),
        const WizardSectionHeader('Volume binding mode'),
        const SizedBox(height: 8),
        SegmentedButton<String>(
          segments: const [
            ButtonSegment(value: 'Immediate', label: Text('Immediate')),
            ButtonSegment(
                value: 'WaitForFirstConsumer',
                label: Text('Wait for consumer')),
          ],
          selected: {state.form.volumeBindingMode},
          showSelectedIcon: false,
          onSelectionChanged: (s) => controller
              .updateForm((f) => f.copyWith(volumeBindingMode: s.first)),
        ),
        if (stepErrors['volumeBindingMode'] != null)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Text(
              stepErrors['volumeBindingMode']!,
              style: TextStyle(color: colors.error, fontSize: 12),
            ),
          ),
        const SizedBox(height: 16),
        SwitchListTile(
          title: const Text('Allow volume expansion'),
          value: state.form.allowVolumeExpansion,
          contentPadding: EdgeInsets.zero,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(allowVolumeExpansion: v)),
        ),
        SwitchListTile(
          title: const Text('Mark as default StorageClass'),
          subtitle: const Text(
              'Adds storageclass.kubernetes.io/is-default-class annotation'),
          value: state.form.isDefault,
          contentPadding: EdgeInsets.zero,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(isDefault: v)),
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader(
          'Parameters',
          subtitle: 'Provisioner-specific (e.g. type=gp3, fsType=ext4)',
        ),
        const SizedBox(height: 8),
        KeyValueTable(
          pairs: state.form.parameters,
          onChanged: (kv) =>
              controller.updateForm((f) => f.copyWith(parameters: kv)),
          keyLabel: 'Key',
          valueLabel: 'Value',
          errorMessage: stepErrors['parameters'],
        ),
        const SizedBox(height: 16),
        Text(
          'Mount options (one per line, optional)',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
        const SizedBox(height: 4),
        TextFormField(
          initialValue: state.form.mountOptions,
          maxLines: 3,
          decoration: const InputDecoration(
            hintText: 'noatime\nnodiratime',
            border: OutlineInputBorder(),
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(mountOptions: v)),
        ),
      ],
    );
  }
}
