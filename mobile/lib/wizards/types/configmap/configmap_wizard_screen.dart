// ConfigMap wizard screen. Wraps WizardScreenScaffold with the two
// step builders — Configure (form fields) and Review (YAML preview).
//
// Cluster pinning happens here: capture activeClusterProvider once at
// first build (via `_pinnedCluster` late final), pass into WizardKey,
// the controller compares to active on preview/apply.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../cluster/cluster_provider.dart';
import '../../../routing/domain_sections.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../widgets/key_value_table.dart';
import '../../widgets/wizard_screen_scaffold.dart';
import '../../widgets/yaml_preview_panel.dart';
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
      title: 'New ConfigMap',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: configMapWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => _ReviewStep(wizardKey: _wizardKey),
      ],
      onApplied: (ctx, outcome) {
        ScaffoldMessenger.of(ctx).showSnackBar(
          SnackBar(
            content: Text(
              outcome.created > 0
                  ? 'Created ConfigMap "${outcome.firstResultName}"'
                  : 'Apply complete',
            ),
          ),
        );
        // Navigate to the created ConfigMap's detail screen on tablet,
        // back to the list on phone. Both are fine UX here, so route
        // unconditionally to detail — the operator can pop back.
        if (outcome.firstResultName.isNotEmpty &&
            outcome.firstResultNamespace != null) {
          ctx.go(kindDetailPath(
            clusterId: _wizardKey.clusterId,
            kind: 'configmaps',
            namespace: outcome.firstResultNamespace!,
            name: outcome.firstResultName,
          ));
        } else {
          ctx.go('/clusters/${_wizardKey.clusterId}/config/configmaps');
        }
      },
    );
  }
}

class _ConfigureStep extends ConsumerWidget {
  const _ConfigureStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(configMapWizardProvider(wizardKey));
    final controller = ref.read(configMapWizardProvider(wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
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

class _ReviewStep extends ConsumerWidget {
  const _ReviewStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(configMapWizardProvider(wizardKey));
    final controller =
        ref.read(configMapWizardProvider(wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;

    if (state.status == WizardStatus.applied &&
        state.applyOutcome != null) {
      final outcome = state.applyOutcome!;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(
              color: colors.success.withValues(alpha: 0.10),
              border: Border.all(color: colors.success.withValues(alpha: 0.4)),
              borderRadius: BorderRadius.circular(8),
            ),
            child: Row(
              children: [
                Icon(Icons.check_circle_outline, color: colors.success),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        'Apply complete',
                        style: TextStyle(
                          color: colors.textPrimary,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      Text(
                        '${outcome.created} created · '
                        '${outcome.configured} configured · '
                        '${outcome.unchanged} unchanged · '
                        '${outcome.failed} failed',
                        style: TextStyle(
                          color: colors.textSecondary,
                          fontSize: 12,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 16),
          YamlPreviewPanel(yaml: state.previewYaml ?? ''),
        ],
      );
    }

    return YamlPreviewPanel(
      yaml: state.previewYaml ?? '',
      loading: state.status == WizardStatus.previewing,
      errorMessage: state.status == WizardStatus.failed &&
              state.previewYaml == null
          ? state.errorMessage
          : null,
      onRetry: state.status == WizardStatus.failed &&
              state.previewYaml == null
          ? controller.retryPreview
          : null,
    );
  }
}
