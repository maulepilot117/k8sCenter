// Default Review-step body. Extracted from the per-wizard
// `_ReviewStep` widgets that PR-3a duplicated three times. Renders:
//
//   * The success card when the wizard transitions to `applied`,
//     showing the apply summary counts.
//   * A loading panel while preview is in flight.
//   * An error banner with Retry when preview failed before producing
//     YAML.
//   * The read-only YAML preview otherwise.
//
// Wizards consume this by passing it as the last entry in their step
// builders list (replacing the per-wizard `_ReviewStep` private
// widget). Per-wizard customization stays possible: pass any extra
// header widgets via the `header` slot (none in PR-3a, used by
// multi-resource wizards in PR-3c).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../theme/kube_theme_builder.dart';
import '../wizard_controller.dart';
import 'yaml_preview_panel.dart';

class WizardReviewBody<TForm> extends ConsumerWidget {
  const WizardReviewBody({
    super.key,
    required this.wizardKey,
    required this.controllerProvider,
    this.header,
  });

  final WizardKey wizardKey;
  final AutoDisposeNotifierProviderFamily<WizardController<TForm>,
      WizardState<TForm>, WizardKey> controllerProvider;
  final Widget? header;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(controllerProvider(wizardKey));
    final controller = ref.read(controllerProvider(wizardKey).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;

    final children = <Widget>[
      ?header,
      if (state.status == WizardStatus.applied &&
          state.applyOutcome != null)
        Padding(
          padding: const EdgeInsets.only(bottom: 16),
          child: _SuccessCard(outcome: state.applyOutcome!, colors: colors),
        ),
      YamlPreviewPanel(
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
      ),
    ];

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: children,
    );
  }
}

class _SuccessCard extends StatelessWidget {
  const _SuccessCard({required this.outcome, required this.colors});

  final WizardApplyOutcome outcome;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
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
    );
  }
}
