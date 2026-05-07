// Secret wizard screen. Same shape as ConfigMap's screen — adds the
// Type dropdown and obscures value rows so reading over an operator's
// shoulder doesn't betray the secret. The reveal toggle on the
// existing detail screen handles post-creation reveal; the wizard's
// editor doesn't double as a reveal surface.

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
import 'secret_wizard_controller.dart';

class SecretWizardScreen extends ConsumerStatefulWidget {
  const SecretWizardScreen({super.key});

  @override
  ConsumerState<SecretWizardScreen> createState() =>
      _SecretWizardScreenState();
}

class _SecretWizardScreenState
    extends ConsumerState<SecretWizardScreen> {
  late final WizardKey _wizardKey =
      WizardKey(clusterId: ref.read(activeClusterProvider));

  @override
  Widget build(BuildContext context) {
    return WizardScreenScaffold<SecretForm>(
      title: 'New Secret',
      subtitle: 'cluster: ${_wizardKey.clusterId}',
      wizardKey: _wizardKey,
      controllerProvider: secretWizardProvider,
      stepBuilders: [
        (ctx) => _ConfigureStep(wizardKey: _wizardKey),
        (ctx) => _ReviewStep(wizardKey: _wizardKey),
      ],
      onApplied: (ctx, outcome) {
        ScaffoldMessenger.of(ctx).showSnackBar(
          SnackBar(
            content: Text(outcome.created > 0
                ? 'Created Secret "${outcome.firstResultName}"'
                : 'Apply complete'),
          ),
        );
        if (outcome.firstResultName.isNotEmpty &&
            outcome.firstResultNamespace != null) {
          ctx.go(kindDetailPath(
            clusterId: _wizardKey.clusterId,
            kind: 'secrets',
            namespace: outcome.firstResultNamespace!,
            name: outcome.firstResultName,
          ));
        } else {
          ctx.go('/clusters/${_wizardKey.clusterId}/config/secrets');
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
    final state = ref.watch(secretWizardProvider(wizardKey));
    final controller = ref.read(secretWizardProvider(wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final stepErrors = state.stepErrors[0] ?? const <String, String>{};

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        TextFormField(
          initialValue: state.form.name,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: 'my-secret',
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
        DropdownButtonFormField<String>(
          initialValue: state.form.type,
          decoration: InputDecoration(
            labelText: 'Type',
            border: const OutlineInputBorder(),
            errorText: stepErrors['type'],
          ),
          items: [
            for (final t in kSecretTypes)
              DropdownMenuItem(value: t, child: Text(t)),
          ],
          onChanged: (v) {
            if (v == null) return;
            controller.updateForm((f) => f.copyWith(type: v));
          },
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
          _hintFor(state.form.type),
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
        const SizedBox(height: 12),
        KeyValueTable(
          pairs: state.form.data,
          onChanged: (pairs) =>
              controller.updateForm((f) => f.copyWith(data: pairs)),
          valueIsObscured: true,
          errorMessage: stepErrors['data'] ??
              _firstDataError(stepErrors),
        ),
      ],
    );
  }

  String _hintFor(String type) {
    switch (type) {
      case 'kubernetes.io/tls':
        return 'TLS secrets require tls.crt and tls.key. Paste raw PEM — '
            'the backend handles base64 encoding.';
      case 'kubernetes.io/basic-auth':
        return 'Basic-auth secrets require username (and usually password). '
            'Paste raw values — the backend handles base64 encoding.';
      case 'kubernetes.io/dockerconfigjson':
        return 'Provide a `.dockerconfigjson` entry with the raw Docker '
            'config JSON. The backend handles base64 encoding.';
      default:
        return 'Each row becomes one entry. Values are typed raw — the '
            'backend base64-encodes them before applying.';
    }
  }

  static String? _firstDataError(Map<String, String> errors) {
    for (final entry in errors.entries) {
      if (entry.key.startsWith('data.') || entry.key.startsWith('data[')) {
        return entry.value;
      }
    }
    return null;
  }
}

class _ReviewStep extends ConsumerWidget {
  const _ReviewStep({required this.wizardKey});
  final WizardKey wizardKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(secretWizardProvider(wizardKey));
    final controller = ref.read(secretWizardProvider(wizardKey).notifier);
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
                  child: Text(
                    '${outcome.created} created · '
                    '${outcome.configured} configured · '
                    '${outcome.unchanged} unchanged · '
                    '${outcome.failed} failed',
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 13,
                    ),
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
