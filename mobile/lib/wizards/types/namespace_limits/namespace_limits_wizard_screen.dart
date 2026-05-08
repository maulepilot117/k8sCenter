// NamespaceLimits wizard screen. One Configure step composes the
// namespace + two resource names + ResourceQuota fields + four
// container LimitRange ResourcePairs. Preview YAML is multi-doc;
// apply summary card already shows aggregate counts (created /
// configured / failed) that work for two-doc applies without
// changes.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/section_header.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'namespace_limits_wizard_controller.dart';

class NamespaceLimitsWizardScreen extends ConsumerStatefulWidget {
  const NamespaceLimitsWizardScreen({super.key});

  @override
  ConsumerState<NamespaceLimitsWizardScreen> createState() =>
      _NamespaceLimitsWizardScreenState();
}

class _NamespaceLimitsWizardScreenState
    extends ConsumerState<NamespaceLimitsWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<NamespaceLimitsForm>(
      wizardType: 'namespace-limits',
      title: 'New namespace limits',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: namespaceLimitsWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<NamespaceLimitsForm>(
              wizardKey: _wizardKey,
              controllerProvider: namespaceLimitsWizardProvider,
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
    final state = ref.watch(namespaceLimitsWizardProvider(wizardKey));
    final controller =
        ref.read(namespaceLimitsWizardProvider(wizardKey).notifier);
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.namespace,
          decoration: InputDecoration(
            labelText: 'Namespace',
            hintText: 'tenant-a',
            border: const OutlineInputBorder(),
            errorText: stepErrors['namespace'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(namespace: v.trim())),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.quotaName,
          decoration: InputDecoration(
            labelText: 'ResourceQuota name',
            hintText: 'tenant-a-quota',
            border: const OutlineInputBorder(),
            errorText: stepErrors['quotaName'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(quotaName: v.trim())),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.limitRangeName,
          decoration: InputDecoration(
            labelText: 'LimitRange name',
            hintText: 'tenant-a-limits',
            border: const OutlineInputBorder(),
            errorText: stepErrors['limitRangeName'],
          ),
          onChanged: (v) => controller
              .updateForm((f) => f.copyWith(limitRangeName: v.trim())),
        ),
        const SizedBox(height: 24),
        WizardSectionHeader('ResourceQuota — namespace caps'),
        const SizedBox(height: 8),
        TextFormField(
          initialValue: state.form.cpuHard,
          decoration: InputDecoration(
            labelText: 'Total CPU',
            hintText: '4 or 4000m',
            border: const OutlineInputBorder(),
            errorText: stepErrors['quota.cpuHard'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(cpuHard: v.trim())),
        ),
        const SizedBox(height: 12),
        TextFormField(
          initialValue: state.form.memoryHard,
          decoration: InputDecoration(
            labelText: 'Total memory',
            hintText: '16Gi',
            border: const OutlineInputBorder(),
            errorText: stepErrors['quota.memoryHard'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(memoryHard: v.trim())),
        ),
        const SizedBox(height: 12),
        _IntField(
          label: 'Maximum pods',
          hint: '100',
          value: state.form.podsHard,
          error: stepErrors['quota.podsHard'],
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(podsHard: v)),
        ),
        const SizedBox(height: 24),
        WizardSectionHeader(
          'LimitRange — per-container defaults & bounds',
          subtitle: 'Each container in the namespace inherits these',
        ),
        const SizedBox(height: 8),
        _ResourcePairFields(
          title: 'Default limit',
          pair: state.form.containerDefault,
          stepErrors: stepErrors,
          fieldPrefix: 'limits.containerDefault',
          onChanged: (p) =>
              controller.updateForm((f) => f.copyWith(containerDefault: p)),
        ),
        const SizedBox(height: 16),
        _ResourcePairFields(
          title: 'Default request',
          pair: state.form.containerDefaultRequest,
          stepErrors: stepErrors,
          fieldPrefix: 'limits.containerDefaultRequest',
          onChanged: (p) => controller.updateForm(
              (f) => f.copyWith(containerDefaultRequest: p)),
        ),
        const SizedBox(height: 16),
        _ResourcePairFields(
          title: 'Max',
          pair: state.form.containerMax,
          stepErrors: stepErrors,
          fieldPrefix: 'limits.containerMax',
          onChanged: (p) =>
              controller.updateForm((f) => f.copyWith(containerMax: p)),
        ),
        const SizedBox(height: 16),
        _ResourcePairFields(
          title: 'Min',
          pair: state.form.containerMin,
          stepErrors: stepErrors,
          fieldPrefix: 'limits.containerMin',
          onChanged: (p) =>
              controller.updateForm((f) => f.copyWith(containerMin: p)),
        ),
        const SizedBox(height: 16),
        Text(
          'Optional fields (PVC limits, GPU quota, custom thresholds) — '
          'edit the YAML directly after applying.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
      ],
    );
  }
}

class _IntField extends StatefulWidget {
  const _IntField({
    required this.label,
    required this.hint,
    required this.value,
    required this.onChanged,
    this.error,
  });

  final String label;
  final String hint;
  final int value;
  final String? error;
  final ValueChanged<int> onChanged;

  @override
  State<_IntField> createState() => _IntFieldState();
}

class _IntFieldState extends State<_IntField> {
  late final TextEditingController _ctl = TextEditingController(
      text: widget.value == 0 ? '' : '${widget.value}');

  @override
  void didUpdateWidget(covariant _IntField old) {
    super.didUpdateWidget(old);
    final next = widget.value == 0 ? '' : '${widget.value}';
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
        final n = int.tryParse(v) ?? 0;
        widget.onChanged(n);
      },
      decoration: InputDecoration(
        labelText: widget.label,
        hintText: widget.hint,
        border: const OutlineInputBorder(),
        errorText: widget.error,
      ),
    );
  }
}

class _ResourcePairFields extends StatelessWidget {
  const _ResourcePairFields({
    required this.title,
    required this.pair,
    required this.stepErrors,
    required this.fieldPrefix,
    required this.onChanged,
  });

  final String title;
  final ResourcePair pair;
  final Map<String, String> stepErrors;
  final String fieldPrefix;
  final ValueChanged<ResourcePair> onChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title,
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 13,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 4),
        Row(
          children: [
            Expanded(
              child: TextFormField(
                initialValue: pair.cpu,
                decoration: InputDecoration(
                  labelText: 'CPU',
                  hintText: '200m',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: stepErrors['$fieldPrefix.cpu'],
                ),
                onChanged: (v) => onChanged(pair.copyWith(cpu: v.trim())),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: TextFormField(
                initialValue: pair.memory,
                decoration: InputDecoration(
                  labelText: 'Memory',
                  hintText: '256Mi',
                  isDense: true,
                  border: const OutlineInputBorder(),
                  errorText: stepErrors['$fieldPrefix.memory'],
                ),
                onChanged: (v) =>
                    onChanged(pair.copyWith(memory: v.trim())),
              ),
            ),
          ],
        ),
      ],
    );
  }
}
