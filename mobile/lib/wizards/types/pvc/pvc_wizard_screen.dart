// PVC wizard screen. Single Configure step (name + namespace +
// storageclass picker + size value/unit + access-mode radios) → Review.

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
import 'pvc_wizard_controller.dart';

class PvcWizardScreen extends ConsumerStatefulWidget {
  const PvcWizardScreen({super.key});

  @override
  ConsumerState<PvcWizardScreen> createState() => _PvcWizardScreenState();
}

class _PvcWizardScreenState extends ConsumerState<PvcWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<PvcForm>(
      wizardType: 'pvc',
      title: 'New PersistentVolumeClaim',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: pvcWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<PvcForm>(
              wizardKey: _wizardKey,
              controllerProvider: pvcWizardProvider,
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
    final state = ref.watch(pvcWizardProvider(wizardKey));
    final controller = ref.read(pvcWizardProvider(wizardKey).notifier);
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
            hintText: 'my-data',
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
          kind: 'storageclasses',
          namespace: null,
          selected: state.form.storageClassName,
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(storageClassName: v)),
          label: 'Storage class',
          hint: 'Pick a class',
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
              DropdownMenuItem(
                value: mode,
                child: Text(
                  '$mode — ${_accessModeDesc(mode)}',
                  overflow: TextOverflow.ellipsis,
                ),
              ),
          ],
          onChanged: (v) {
            if (v == null) return;
            controller.updateForm((f) => f.copyWith(accessMode: v));
          },
        ),
        if (state.form.dataSource != null) ...[
          const SizedBox(height: 16),
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: colors.accent.withValues(alpha: 0.08),
              border: Border.all(color: colors.accent.withValues(alpha: 0.2)),
              borderRadius: BorderRadius.circular(6),
            ),
            child: Text(
              'Restoring from snapshot: ${state.form.dataSource!.name}',
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ],
      ],
    );
  }

  String _accessModeDesc(String m) {
    switch (m) {
      case 'ReadWriteOnce':
        return 'Mounted read-write by a single node';
      case 'ReadOnlyMany':
        return 'Mounted read-only by many nodes';
      case 'ReadWriteMany':
        return 'Mounted read-write by many nodes';
      case 'ReadWriteOncePod':
        return 'Mounted read-write by a single pod';
      default:
        return '';
    }
  }
}
