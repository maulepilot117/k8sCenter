// HPA wizard screen. Configure step composes name + namespace +
// target picker (KindPicker for kind, NamedResourcePicker scoped to
// the active namespace for name) + min/max replicas (OptionalIntField
// for min — blank means "let HPA defaults apply") + a metrics
// RepeatingRowGroup.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/kind_picker.dart';
import '../../widgets/named_resource_picker.dart';
import '../../widgets/optional_int_field.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'hpa_wizard_controller.dart';

class HpaWizardScreen extends ConsumerStatefulWidget {
  const HpaWizardScreen({super.key});

  @override
  ConsumerState<HpaWizardScreen> createState() => _HpaWizardScreenState();
}

class _HpaWizardScreenState extends ConsumerState<HpaWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<HpaForm>(
      wizardType: 'hpa',
      title: 'New HorizontalPodAutoscaler',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: hpaWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<HpaForm>(
              wizardKey: _wizardKey,
              controllerProvider: hpaWizardProvider,
            ),
      ],
    );
  }
}

class _ConfigureStep extends ConsumerWidget {
  const _ConfigureStep({required this.wizardKey});
  final WizardKey wizardKey;

  /// Maps `targetKind` (singular) to the resource list kind (lowercase
  /// plural) the named picker fetches.
  String _kindToList(String tk) {
    switch (tk) {
      case 'Deployment':
        return 'deployments';
      case 'StatefulSet':
        return 'statefulsets';
      case 'ReplicaSet':
        return 'replicasets';
      default:
        return 'deployments';
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(hpaWizardProvider(wizardKey));
    final controller = ref.read(hpaWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'web-hpa',
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
        const SizedBox(height: 24),
        WizardSectionHeader('Scale target', subtitle: 'Workload to autoscale'),
        const SizedBox(height: 8),
        KindPicker(
          options: [
            for (final kind in kHpaTargetKinds)
              KindPickerOption(value: kind, label: kind),
          ],
          selected: state.form.targetKind,
          onChanged: (v) {
            // Switching kind clears the name — a Deployment named X
            // probably isn't a StatefulSet named X.
            controller.updateForm((f) => f.copyWith(
                  targetKind: v,
                  targetName: '',
                ));
          },
          errorMessage: stepErrors['targetKind'],
        ),
        const SizedBox(height: 12),
        if (state.form.namespace.trim().isEmpty)
          Text(
            'Pick a namespace first to load target ${_kindToList(state.form.targetKind)}.',
            style: TextStyle(
              color:
                  Theme.of(context).extension<KubeColors>()!.textMuted,
              fontSize: 12,
            ),
          )
        else
          NamedResourcePicker(
            clusterId: wizardKey.clusterId,
            kind: _kindToList(state.form.targetKind),
            namespace: state.form.namespace.trim(),
            selected: state.form.targetName,
            onChanged: (v) =>
                controller.updateForm((f) => f.copyWith(targetName: v)),
            label: 'Target name',
            hint: 'Pick a ${state.form.targetKind.toLowerCase()}',
            errorMessage: stepErrors['targetName'],
          ),
        const SizedBox(height: 24),
        WizardSectionHeader('Replica bounds'),
        const SizedBox(height: 8),
        Row(
          children: [
            Expanded(
              child: OptionalIntField(
                label: 'minReplicas',
                hint: 'optional',
                value: state.form.minReplicas,
                error: stepErrors['minReplicas'],
                onChanged: (v) {
                  if (v == null) {
                    controller.updateForm(
                        (f) => f.copyWith(clearMinReplicas: true));
                    return;
                  }
                  controller.updateForm((f) => f.copyWith(minReplicas: v));
                },
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: _MaxReplicasField(
                value: state.form.maxReplicas,
                error: stepErrors['maxReplicas'],
                onChanged: (v) =>
                    controller.updateForm((f) => f.copyWith(maxReplicas: v)),
              ),
            ),
          ],
        ),
        const SizedBox(height: 24),
        WizardSectionHeader('Metrics', subtitle: 'Each row is a Resource metric'),
        const SizedBox(height: 8),
        RepeatingRowGroup<HpaMetric>(
          items: state.form.metrics,
          addLabel: 'Add metric',
          onAdd: () => controller.updateForm((f) =>
              f.copyWith(metrics: [...f.metrics, const HpaMetric()])),
          onRemove: (i) {
            final next = [...state.form.metrics]..removeAt(i);
            controller.updateForm((f) => f.copyWith(metrics: next));
          },
          itemBuilder: (ctx, i, m) => _MetricRow(
            index: i,
            metric: m,
            stepErrors: stepErrors,
            onChanged: (next) {
              final list = [...state.form.metrics];
              list[i] = next;
              controller.updateForm((f) => f.copyWith(metrics: list));
            },
          ),
          errorMessage: stepErrors['metrics'],
        ),
      ],
    );
  }
}

class _MaxReplicasField extends StatefulWidget {
  const _MaxReplicasField({
    required this.value,
    required this.error,
    required this.onChanged,
  });

  final int value;
  final String? error;
  final ValueChanged<int> onChanged;

  @override
  State<_MaxReplicasField> createState() => _MaxReplicasFieldState();
}

class _MaxReplicasFieldState extends State<_MaxReplicasField> {
  late final TextEditingController _ctl =
      TextEditingController(text: '${widget.value}');

  @override
  void didUpdateWidget(covariant _MaxReplicasField old) {
    super.didUpdateWidget(old);
    final next = '${widget.value}';
    if (_ctl.text != next) _ctl.text = next;
  }

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: _ctl,
      keyboardType: TextInputType.number,
      inputFormatters: [FilteringTextInputFormatter.digitsOnly],
      onChanged: (v) {
        final n = int.tryParse(v);
        if (n != null) widget.onChanged(n);
      },
      decoration: InputDecoration(
        labelText: 'maxReplicas',
        hintText: '10',
        border: const OutlineInputBorder(),
        errorText: widget.error,
      ),
    );
  }
}

class _MetricRow extends StatefulWidget {
  const _MetricRow({
    required this.index,
    required this.metric,
    required this.stepErrors,
    required this.onChanged,
  });

  final int index;
  final HpaMetric metric;
  final Map<String, String> stepErrors;
  final ValueChanged<HpaMetric> onChanged;

  @override
  State<_MetricRow> createState() => _MetricRowState();
}

class _MetricRowState extends State<_MetricRow> {
  late final TextEditingController _val = TextEditingController(
      text: '${widget.metric.targetAverageValue}');

  @override
  void didUpdateWidget(covariant _MetricRow old) {
    super.didUpdateWidget(old);
    final next = '${widget.metric.targetAverageValue}';
    if (_val.text != next) _val.text = next;
  }

  @override
  void dispose() {
    _val.dispose();
    super.dispose();
  }

  String? _err(String suffix) =>
      widget.stepErrors['metrics[${widget.index}]$suffix'];

  @override
  Widget build(BuildContext context) {
    final isUtilization = widget.metric.targetType == 'Utilization';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              flex: 4,
              child: DropdownButtonFormField<String>(
                initialValue: widget.metric.resourceName,
                isExpanded: true,
                decoration: InputDecoration(
                  labelText: 'Resource',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: _err('.resourceName'),
                ),
                items: [
                  for (final r in kHpaResourceNames)
                    DropdownMenuItem(value: r, child: Text(r)),
                ],
                onChanged: (v) {
                  if (v == null) return;
                  widget.onChanged(widget.metric.copyWith(resourceName: v));
                },
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              flex: 5,
              child: DropdownButtonFormField<String>(
                initialValue: widget.metric.targetType,
                isExpanded: true,
                decoration: InputDecoration(
                  labelText: 'Target',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: _err('.targetType'),
                ),
                items: [
                  for (final t in kHpaTargetTypes)
                    DropdownMenuItem(value: t, child: Text(t)),
                ],
                onChanged: (v) {
                  if (v == null) return;
                  widget.onChanged(widget.metric.copyWith(targetType: v));
                },
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),
        TextField(
          controller: _val,
          keyboardType: TextInputType.number,
          inputFormatters: [FilteringTextInputFormatter.digitsOnly],
          onChanged: (v) {
            final n = int.tryParse(v);
            if (n != null) {
              widget.onChanged(widget.metric.copyWith(targetAverageValue: n));
            }
          },
          decoration: InputDecoration(
            labelText: isUtilization ? 'Target % utilization' : 'Average value',
            hintText: isUtilization ? '80' : '500',
            isDense: true,
            border: const OutlineInputBorder(),
            errorText: _err('.targetAverageValue'),
          ),
        ),
      ],
    );
  }
}
