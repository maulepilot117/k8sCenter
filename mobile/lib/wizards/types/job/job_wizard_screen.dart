// Job wizard screen — single Configure step + shared Review body.

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
import 'job_wizard_controller.dart';

class JobWizardScreen extends ConsumerStatefulWidget {
  const JobWizardScreen({super.key});

  @override
  ConsumerState<JobWizardScreen> createState() => _JobWizardScreenState();
}

class _JobWizardScreenState extends ConsumerState<JobWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<JobForm>(
      wizardType: 'job',
      title: 'New Job',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: jobWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<JobForm>(
              wizardKey: _wizardKey,
              controllerProvider: jobWizardProvider,
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
    final state = ref.watch(jobWizardProvider(wizardKey));
    final controller = ref.read(jobWizardProvider(wizardKey).notifier);
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
            hintText: 'my-job',
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
          initialValue: state.form.image,
          decoration: InputDecoration(
            labelText: 'Image',
            hintText: 'busybox:latest',
            border: const OutlineInputBorder(),
            errorText: errors['container.image'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(image: v)),
        ),
        const SizedBox(height: 16),
        DropdownButtonFormField<String>(
          initialValue: state.form.restartPolicy,
          items: const [
            DropdownMenuItem(value: 'Never', child: Text('Never')),
            DropdownMenuItem(value: 'OnFailure', child: Text('OnFailure')),
          ],
          onChanged: (v) {
            if (v == null) return;
            controller.updateForm((f) => f.copyWith(restartPolicy: v));
          },
          decoration: InputDecoration(
            labelText: 'Restart policy',
            border: const OutlineInputBorder(),
            errorText: errors['restartPolicy'],
          ),
        ),
        const SizedBox(height: 16),
        Row(
          children: [
            Expanded(
              child: _OptionalIntField(
                label: 'Parallelism',
                hint: '1',
                value: state.form.parallelism,
                error: errors['parallelism'],
                onChanged: (n) => controller
                    .updateForm((f) => f.copyWith(parallelism: n)),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: _OptionalIntField(
                label: 'Completions',
                hint: '1',
                value: state.form.completions,
                error: errors['completions'],
                onChanged: (n) => controller
                    .updateForm((f) => f.copyWith(completions: n)),
              ),
            ),
          ],
        ),
        const SizedBox(height: 16),
        _OptionalIntField(
          label: 'Backoff limit',
          hint: '6',
          value: state.form.backoffLimit,
          error: errors['backoffLimit'],
          onChanged: (n) =>
              controller.updateForm((f) => f.copyWith(backoffLimit: n)),
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

/// Numeric field where blank means "leave field unset" (backend treats
/// nil pointers as defaults). Avoids the trap where 0 is a valid value
/// but blank should mean "omit".
class _OptionalIntField extends StatefulWidget {
  const _OptionalIntField({
    required this.label,
    required this.hint,
    required this.value,
    required this.onChanged,
    this.error,
  });

  final String label;
  final String hint;
  final int? value;
  final String? error;
  final ValueChanged<int?> onChanged;

  @override
  State<_OptionalIntField> createState() => _OptionalIntFieldState();
}

class _OptionalIntFieldState extends State<_OptionalIntField> {
  late final TextEditingController _ctl =
      TextEditingController(text: widget.value?.toString() ?? '');

  @override
  void didUpdateWidget(covariant _OptionalIntField oldWidget) {
    super.didUpdateWidget(oldWidget);
    final next = widget.value?.toString() ?? '';
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
      onChanged: (v) {
        final s = v.trim();
        if (s.isEmpty) {
          widget.onChanged(null);
          return;
        }
        final n = int.tryParse(s);
        if (n == null) return;
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
