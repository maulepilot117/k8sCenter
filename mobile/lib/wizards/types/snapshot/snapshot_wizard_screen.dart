// VolumeSnapshot wizard screen. Configure step composes name +
// namespace + source PVC picker (scoped to the wizard's namespace) +
// optional snapshot class name (free text — VolumeSnapshotClass list
// isn't always queryable per-cluster, so a typed input is the safe
// default).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../widgets/named_resource_picker.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'snapshot_wizard_controller.dart';

class SnapshotWizardScreen extends ConsumerStatefulWidget {
  const SnapshotWizardScreen({super.key});

  @override
  ConsumerState<SnapshotWizardScreen> createState() =>
      _SnapshotWizardScreenState();
}

class _SnapshotWizardScreenState extends ConsumerState<SnapshotWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<SnapshotForm>(
      wizardType: 'snapshot',
      title: 'New VolumeSnapshot',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: snapshotWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<SnapshotForm>(
              wizardKey: _wizardKey,
              controllerProvider: snapshotWizardProvider,
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
    final state = ref.watch(snapshotWizardProvider(wizardKey));
    final controller = ref.read(snapshotWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'data-snap-2026-05-08',
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
        // Picker is scoped to the wizard's currently-typed namespace.
        // Empty namespace yields an empty list (the picker shows a
        // helpful empty-state message).
        NamedResourcePicker(
          clusterId: wizardKey.clusterId,
          kind: 'persistentvolumeclaims',
          namespace: state.form.namespace.isEmpty ? null : state.form.namespace,
          selected: state.form.sourcePVC,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(sourcePVC: v)),
          label: 'Source PVC',
          hint: 'Pick a PVC in this namespace',
          errorMessage: stepErrors['sourcePVC'],
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.volumeSnapshotClassName,
          decoration: InputDecoration(
            labelText: 'VolumeSnapshotClass (optional)',
            hintText: 'csi-hostpath-snapclass',
            border: const OutlineInputBorder(),
            errorText: stepErrors['volumeSnapshotClassName'],
            helperText: 'Defaults to the cluster default if omitted',
          ),
          onChanged: (v) => controller.updateForm(
              (f) => f.copyWith(volumeSnapshotClassName: v.trim())),
        ),
      ],
    );
  }
}
