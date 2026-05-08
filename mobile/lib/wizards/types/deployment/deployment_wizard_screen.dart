// Deployment wizard screen — 4 steps. Mirrors web `DeploymentWizard.tsx`.
// The Configure flow is split across three form steps:
//
//   Step 0 Basics    — name + namespace + image + replicas + labels
//                      + env vars
//   Step 1 Networking — container ports
//   Step 2 Resources  — CPU/memory + liveness + readiness probes
//   Step 3 Review     — YAML preview + apply (delegates to
//                      WizardReviewBody)

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/container_form_parts.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/probe_form.dart';
import '../../widgets/repeating_row_group.dart';
import '../../widgets/resources_form.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'deployment_wizard_controller.dart';

class DeploymentWizardScreen extends ConsumerStatefulWidget {
  const DeploymentWizardScreen({super.key});

  @override
  ConsumerState<DeploymentWizardScreen> createState() =>
      _DeploymentWizardScreenState();
}

class _DeploymentWizardScreenState
    extends ConsumerState<DeploymentWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<DeploymentForm>(
      wizardType: 'deployment',
      title: 'New Deployment',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: deploymentWizardProvider,
      stepBuilders: [
        (ctx) => _BasicsStep(wizardKey: _wizardKey),
        (ctx) => _NetworkingStep(wizardKey: _wizardKey),
        (ctx) => _ResourcesStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<DeploymentForm>(
              wizardKey: _wizardKey,
              controllerProvider: deploymentWizardProvider,
            ),
      ],
    );
  }
}

class _BasicsStep extends ConsumerWidget {
  const _BasicsStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(deploymentWizardProvider(wizardKey));
    final controller =
        ref.read(deploymentWizardProvider(wizardKey).notifier);
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
            hintText: 'my-app',
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
            hintText: 'nginx:1.27',
            border: const OutlineInputBorder(),
            errorText: errors['image'],
          ),
          onChanged: (v) =>
              controller.updateForm((f) => f.copyWith(image: v)),
        ),
        const SizedBox(height: 16),
        TextFormField(
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
              controller.updateForm((f) => f.copyWith(replicas: n));
            }
          },
        ),
        const SizedBox(height: 24),
        Text(
          'Labels',
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 4),
        Text(
          'Optional. The wizard adds `app: <name>` automatically.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
        const SizedBox(height: 12),
        KeyValueTable(
          pairs: state.form.labels,
          onChanged: (pairs) =>
              controller.updateForm((f) => f.copyWith(labels: pairs)),
          errorMessage: errors['labels'],
        ),
        const SizedBox(height: 24),
        EnvVarSection(
          items: state.form.envVars,
          onChanged: (list) =>
              controller.updateForm((f) => f.copyWith(envVars: list)),
          errorMessage: errors['envVars'],
        ),
      ],
    );
  }
}

/// Map form-row index to the index the backend reports errors against
/// for `ports[N]` paths, accounting for `containerPortsAsJson()`
/// stripping empty rows. Returns null when the row at [formIndex] is
/// itself empty.
int? _portsServerIndexFor(List<ContainerPortData> rows, int formIndex) {
  if (formIndex < 0 || formIndex >= rows.length) return null;
  if (rows[formIndex].isEmpty) return null;
  var serverIndex = 0;
  for (var i = 0; i < formIndex; i++) {
    if (!rows[i].isEmpty) serverIndex++;
  }
  return serverIndex;
}

class _NetworkingStep extends ConsumerWidget {
  const _NetworkingStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(deploymentWizardProvider(wizardKey));
    final controller =
        ref.read(deploymentWizardProvider(wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final errors = state.stepErrors[1] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        Text(
          'Container ports',
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 4),
        Text(
          'Ports the container listens on. To expose them outside the '
          'pod, create a Service in a separate wizard.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
        const SizedBox(height: 12),
        RepeatingRowGroup<ContainerPortData>(
          items: state.form.ports,
          itemBuilder: (ctx, i, item) {
            // containerPortsAsJson() strips empty rows before send,
            // so server-reported errors are indexed against the
            // stripped list. Map form-row index → server index so
            // the error lands on the row the operator actually filled.
            final serverIndex =
                _portsServerIndexFor(state.form.ports, i);
            return ContainerPortRow(
              value: item,
              portError: serverIndex == null
                  ? null
                  : errors['ports[$serverIndex].containerPort'],
              onChanged: (next) {
                final list = [...state.form.ports];
                list[i] = next;
                controller.updateForm((f) => f.copyWith(ports: list));
              },
            );
          },
          onAdd: () => controller.updateForm((f) => f.copyWith(
                ports: [...f.ports, const ContainerPortData()],
              )),
          onRemove: (i) {
            final list = [...state.form.ports]..removeAt(i);
            controller.updateForm((f) => f.copyWith(ports: list));
          },
          addLabel: 'Add port',
          emptyMessage: 'No ports declared.',
          errorMessage: errors['ports'],
        ),
      ],
    );
  }
}

class _ResourcesStep extends ConsumerWidget {
  const _ResourcesStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(deploymentWizardProvider(wizardKey));
    final controller =
        ref.read(deploymentWizardProvider(wizardKey).notifier);
    final errors = state.stepErrors[2] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        ResourcesFormSection(
          resources: state.form.resources,
          fieldErrors: errors,
          onChanged: (next) =>
              controller.updateForm((f) => f.copyWith(resources: next)),
        ),
        const SizedBox(height: 16),
        ProbeFormSection(
          label: 'Liveness probe',
          probe: state.form.liveness,
          fieldErrors: errors,
          fieldPrefix: 'probes.liveness',
          onChanged: (next) =>
              controller.updateForm((f) => f.copyWith(liveness: next)),
        ),
        const SizedBox(height: 12),
        ProbeFormSection(
          label: 'Readiness probe',
          probe: state.form.readiness,
          fieldErrors: errors,
          fieldPrefix: 'probes.readiness',
          onChanged: (next) =>
              controller.updateForm((f) => f.copyWith(readiness: next)),
        ),
      ],
    );
  }
}
