// DaemonSet wizard screen — single Configure step + Review.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/container_form_parts.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/probe_form.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'daemonset_wizard_controller.dart';

class DaemonSetWizardScreen extends ConsumerStatefulWidget {
  const DaemonSetWizardScreen({super.key});

  @override
  ConsumerState<DaemonSetWizardScreen> createState() =>
      _DaemonSetWizardScreenState();
}

class _DaemonSetWizardScreenState
    extends ConsumerState<DaemonSetWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<DaemonSetForm>(
      wizardType: 'daemonset',
      title: 'New DaemonSet',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: daemonSetWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<DaemonSetForm>(
              wizardKey: _wizardKey,
              controllerProvider: daemonSetWizardProvider,
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
    final state = ref.watch(daemonSetWizardProvider(wizardKey));
    final controller =
        ref.read(daemonSetWizardProvider(wizardKey).notifier);
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
            hintText: 'node-exporter',
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
            hintText: 'kube-system',
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
            hintText: 'quay.io/prometheus/node-exporter:latest',
            border: const OutlineInputBorder(),
            errorText: errors['container.image'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(image: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
          initialValue: state.form.maxUnavailable,
          decoration: InputDecoration(
            labelText: 'Max unavailable (optional)',
            hintText: '1 or 25%',
            border: const OutlineInputBorder(),
            errorText: errors['maxUnavailable'],
            helperText: 'Plain int or percent string for rolling updates',
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(maxUnavailable: v)),
        ),
        const SizedBox(height: 24),
        Text(
          'Node selector',
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 4),
        Text(
          'Restrict pods to nodes matching every key/value pair.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
        const SizedBox(height: 12),
        KeyValueTable(
          pairs: state.form.nodeSelector,
          onChanged: (pairs) => controller
              .updateForm((f) => f.copyWith(nodeSelector: pairs)),
          errorMessage: errors['nodeSelector'],
        ),
        const SizedBox(height: 24),
        EnvVarSection(
          items: state.form.envVars,
          onChanged: (list) =>
              controller.updateForm((f) => f.copyWith(envVars: list)),
        ),
        const SizedBox(height: 24),
        ProbeFormSection(
          label: 'Liveness probe',
          probe: state.form.liveness,
          fieldErrors: errors,
          fieldPrefix: 'container.probes.liveness',
          onChanged: (next) =>
              controller.updateForm((f) => f.copyWith(liveness: next)),
        ),
        const SizedBox(height: 12),
        ProbeFormSection(
          label: 'Readiness probe',
          probe: state.form.readiness,
          fieldErrors: errors,
          fieldPrefix: 'container.probes.readiness',
          onChanged: (next) =>
              controller.updateForm((f) => f.copyWith(readiness: next)),
        ),
      ],
    );
  }
}
