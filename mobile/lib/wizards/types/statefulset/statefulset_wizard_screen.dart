// StatefulSet wizard screen — single Configure step + Review.
// VolumeClaimTemplate rows live in their own widget so the form's
// nested fields don't drown the rest of the inputs.

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
import 'statefulset_wizard_controller.dart';

class StatefulSetWizardScreen extends ConsumerStatefulWidget {
  const StatefulSetWizardScreen({super.key});

  @override
  ConsumerState<StatefulSetWizardScreen> createState() =>
      _StatefulSetWizardScreenState();
}

class _StatefulSetWizardScreenState
    extends ConsumerState<StatefulSetWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<StatefulSetForm>(
      wizardType: 'statefulset',
      title: 'New StatefulSet',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: statefulSetWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<StatefulSetForm>(
              wizardKey: _wizardKey,
              controllerProvider: statefulSetWizardProvider,
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
    final state = ref.watch(statefulSetWizardProvider(wizardKey));
    final controller =
        ref.read(statefulSetWizardProvider(wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final errors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'web',
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
          initialValue: state.form.serviceName,
          decoration: InputDecoration(
            labelText: 'Service name',
            hintText: 'web',
            border: const OutlineInputBorder(),
            errorText: errors['serviceName'],
            helperText:
                'The headless Service governing the StatefulSet pods.',
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(serviceName: v)),
        ),
        const SizedBox(height: 16),
        Row(
          children: [
            Expanded(
              child: TextFormField(
                initialValue: state.form.replicas.toString(),
                keyboardType: TextInputType.number,
                decoration: InputDecoration(
                  labelText: 'Replicas',
                  border: const OutlineInputBorder(),
                  errorText: errors['replicas'],
                ),
                onChanged: (v) {
                  final n = int.tryParse(v);
                  if (n != null) {
                    controller
                        .updateForm((f) => f.copyWith(replicas: n));
                  }
                },
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: DropdownButtonFormField<String>(
                initialValue: state.form.podManagementPolicy,
                items: const [
                  DropdownMenuItem(
                      value: 'OrderedReady',
                      child: Text('OrderedReady')),
                  DropdownMenuItem(
                      value: 'Parallel', child: Text('Parallel')),
                ],
                onChanged: (v) {
                  if (v == null) return;
                  controller.updateForm(
                      (f) => f.copyWith(podManagementPolicy: v));
                },
                decoration: InputDecoration(
                  labelText: 'Pod management',
                  border: const OutlineInputBorder(),
                  errorText: errors['podManagementPolicy'],
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.image,
          decoration: InputDecoration(
            labelText: 'Image',
            hintText: 'nginx:1.27',
            border: const OutlineInputBorder(),
            errorText: errors['container.image'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(image: v)),
        ),
        const SizedBox(height: 24),
        Text(
          'Volume claim templates',
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 4),
        Text(
          'Each pod replica gets its own PVC built from these templates.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
        const SizedBox(height: 12),
        RepeatingRowGroup<VolumeClaimTemplate>(
          items: state.form.volumeClaimTemplates,
          itemBuilder: (ctx, i, item) => _VctRow(
            value: item,
            errors: errors,
            index: i,
            onChanged: (next) {
              final list = [...state.form.volumeClaimTemplates];
              list[i] = next;
              controller.updateForm(
                  (f) => f.copyWith(volumeClaimTemplates: list));
            },
          ),
          onAdd: () => controller.updateForm((f) => f.copyWith(
                volumeClaimTemplates: [
                  ...f.volumeClaimTemplates,
                  const VolumeClaimTemplate(),
                ],
              )),
          onRemove: (i) {
            final list = [...state.form.volumeClaimTemplates]..removeAt(i);
            controller.updateForm(
                (f) => f.copyWith(volumeClaimTemplates: list));
          },
          addLabel: 'Add volume claim template',
          emptyMessage:
              'No volume claim templates. The StatefulSet will run '
              'without persistent storage.',
          errorMessage: errors['volumeClaimTemplates'],
        ),
        const SizedBox(height: 24),
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

/// One row inside the volume-claim-template repeating group. Carries
/// its own controllers for name/storageClass/size so focus survives
/// rebuilds; reads error messages keyed by index from the parent.
class _VctRow extends StatefulWidget {
  const _VctRow({
    required this.value,
    required this.errors,
    required this.index,
    required this.onChanged,
  });

  final VolumeClaimTemplate value;
  final Map<String, String> errors;
  final int index;
  final ValueChanged<VolumeClaimTemplate> onChanged;

  @override
  State<_VctRow> createState() => _VctRowState();
}

class _VctRowState extends State<_VctRow> {
  late final TextEditingController _name =
      TextEditingController(text: widget.value.name);
  late final TextEditingController _sc =
      TextEditingController(text: widget.value.storageClassName);
  late final TextEditingController _size =
      TextEditingController(text: widget.value.size);

  @override
  void didUpdateWidget(covariant _VctRow oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_name.text != widget.value.name) _name.text = widget.value.name;
    if (_sc.text != widget.value.storageClassName) {
      _sc.text = widget.value.storageClassName;
    }
    if (_size.text != widget.value.size) _size.text = widget.value.size;
  }

  @override
  void dispose() {
    _name.dispose();
    _sc.dispose();
    _size.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final p = 'volumeClaimTemplates[${widget.index}]';
    return Container(
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        border: Border.all(
            color: Theme.of(context)
                .extension<KubeColors>()!
                .borderSubtle),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          TextField(
            controller: _name,
            onChanged: (v) =>
                widget.onChanged(widget.value.copyWith(name: v)),
            decoration: InputDecoration(
              labelText: 'Name',
              hintText: 'data',
              isDense: true,
              border: const OutlineInputBorder(),
              errorText: widget.errors['$p.name'],
            ),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _sc,
                  onChanged: (v) => widget.onChanged(
                      widget.value.copyWith(storageClassName: v)),
                  decoration: InputDecoration(
                    labelText: 'Storage class (optional)',
                    hintText: 'standard',
                    isDense: true,
                    border: const OutlineInputBorder(),
                    errorText: widget.errors['$p.storageClassName'],
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: TextField(
                  controller: _size,
                  onChanged: (v) =>
                      widget.onChanged(widget.value.copyWith(size: v)),
                  decoration: InputDecoration(
                    labelText: 'Size',
                    hintText: '5Gi',
                    isDense: true,
                    border: const OutlineInputBorder(),
                    errorText: widget.errors['$p.size'],
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          DropdownButtonFormField<String>(
            initialValue: widget.value.accessMode,
            items: const [
              DropdownMenuItem(
                  value: 'ReadWriteOnce', child: Text('ReadWriteOnce')),
              DropdownMenuItem(
                  value: 'ReadWriteMany', child: Text('ReadWriteMany')),
              DropdownMenuItem(
                  value: 'ReadOnlyMany', child: Text('ReadOnlyMany')),
              DropdownMenuItem(
                  value: 'ReadWriteOncePod',
                  child: Text('ReadWriteOncePod')),
            ],
            onChanged: (v) {
              if (v == null) return;
              widget.onChanged(widget.value.copyWith(accessMode: v));
            },
            decoration: InputDecoration(
              labelText: 'Access mode',
              isDense: true,
              border: const OutlineInputBorder(),
              errorText: widget.errors['$p.accessMode'],
            ),
          ),
        ],
      ),
    );
  }
}
