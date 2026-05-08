// ConfigMap wizard screen. Configure step renders form fields; Review
// step delegates to the shared `WizardReviewBody` (preview + apply
// result + retry handling all live there). The scaffold's default
// `onApplied` reads the wizard registry to derive the kind label and
// detail path, removing the per-wizard SnackBar + navigation
// boilerplate.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/wizard_review_body.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/wizard_unrouted_banner.dart';
import '../../wizard_controller.dart';
import 'configmap_wizard_controller.dart';

class ConfigMapWizardScreen extends ConsumerStatefulWidget {
  const ConfigMapWizardScreen({super.key});

  @override
  ConsumerState<ConfigMapWizardScreen> createState() =>
      _ConfigMapWizardScreenState();
}

class _ConfigMapWizardScreenState
    extends ConsumerState<ConfigMapWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<ConfigMapForm>(
      wizardType: 'configmap',
      title: 'New ConfigMap',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: configMapWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => WizardReviewBody<ConfigMapForm>(
              wizardKey: _wizardKey,
              controllerProvider: configMapWizardProvider,
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
    final state = ref.watch(configMapWizardProvider(wizardKey));
    final controller =
        ref.read(configMapWizardProvider(wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        WizardUnroutedBanner(unrouted: state.unrouted),
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'my-config',
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
        Text(
          'Data',
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 4),
        Text(
          'Each row becomes one entry in the ConfigMap. Keys are case-sensitive.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
        const SizedBox(height: 12),
        KeyValueTable(
          pairs: state.form.data,
          onChanged: (pairs) =>
              controller.updateForm((f) => f.copyWith(data: pairs)),
          errorMessage: stepErrors['data'] ??
              // Surface the most-specific data[<key>] error for the
              // first offending row. The KV table widget itself doesn't
              // know which row a server error refers to, so we collapse
              // them into one footer message.
              _collectDataErrors(stepErrors),
        ),
      ],
    );
  }

  /// Collapse multiple `data[<key>]` errors into one displayable line.
  /// First entry wins — operators see the next on retry once they fix
  /// it.
  static String? _collectDataErrors(Map<String, String> errors) {
    for (final entry in errors.entries) {
      if (entry.key.startsWith('data[')) return entry.value;
    }
    return null;
  }
}

