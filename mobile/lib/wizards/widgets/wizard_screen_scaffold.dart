// Generic wizard shell. Hosts the AppBar, the stepper, the per-step
// body, and the Back/Next/Apply footer. Per-wizard screens compose this
// scaffold with their concrete controller provider and step builders.
//
// Cluster pinning lives in the parent screen: parent reads
// `activeClusterProvider` once at first build, captures the id into a
// `WizardKey`, and threads it here. The controller (family-keyed on
// that id) re-checks the active cluster on preview/apply and aborts
// with a clear error on drift — same defense pattern as PR-2's
// `ResourceActionsButton` and `YamlApplyController`.
//
// Footer adapts to the controller status:
//   formEditing  → [Cancel/Back] [Next]
//   previewing   → [Cancel]      [Generating…]
//   reviewing    → [Back]        [Apply]
//   applying     → [Back]        [Applying…]
//   applied      → [Done]                       (single button — pops)
//   failed       → [Cancel]      [Retry]        (Retry re-runs preview
//                                               or apply depending on
//                                               whichever set the
//                                               failed state)

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../theme/kube_theme_builder.dart';
import '../../widgets/confirm_sheet.dart';
import '../wizard_controller.dart';
import 'wizard_stepper_mobile.dart';

/// Builder signature for per-step bodies. Each wizard provides one
/// builder per step — for the Review step, the builder typically
/// returns the YAML preview panel.
typedef WizardStepBuilder = Widget Function(BuildContext context);

class WizardScreenScaffold<TForm> extends ConsumerWidget {
  const WizardScreenScaffold({
    super.key,
    required this.title,
    required this.subtitle,
    required this.wizardKey,
    required this.controllerProvider,
    required this.stepBuilders,
    required this.onApplied,
  });

  final String title;
  final String? subtitle;

  /// Pinned cluster — parent captures `activeClusterProvider` once and
  /// passes it here.
  final WizardKey wizardKey;

  final AutoDisposeNotifierProviderFamily<WizardController<TForm>,
      WizardState<TForm>, WizardKey> controllerProvider;

  /// One builder per step. Length must match the controller's
  /// `steps` length.
  final List<WizardStepBuilder> stepBuilders;

  /// Fired after a successful apply. Wizards typically navigate to the
  /// created resource's detail screen, or back to the resource list.
  final void Function(BuildContext context, WizardApplyOutcome outcome)
      onApplied;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(controllerProvider(wizardKey));
    final controller = ref.read(controllerProvider(wizardKey).notifier);

    // Fire onApplied exactly once on the formEditing/applying → applied
    // transition. Repeat rebuilds skip because prev.status is also
    // applied.
    ref.listen<WizardState<TForm>>(controllerProvider(wizardKey),
        (prev, next) {
      if (prev?.status != WizardStatus.applied &&
          next.status == WizardStatus.applied &&
          next.applyOutcome != null) {
        onApplied(context, next.applyOutcome!);
      }
    });

    final colors = Theme.of(context).extension<KubeColors>()!;
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Text(
              title,
              style: const TextStyle(fontSize: 16),
              overflow: TextOverflow.ellipsis,
            ),
            if (subtitle != null)
              Text(
                subtitle!,
                style: TextStyle(fontSize: 12, color: colors.textMuted),
                overflow: TextOverflow.ellipsis,
              ),
          ],
        ),
        leading: IconButton(
          icon: const Icon(Icons.close),
          onPressed: () => _confirmClose(context, state, controller),
          tooltip: 'Close wizard',
        ),
      ),
      body: SafeArea(
        child: Column(
          children: [
            WizardStepperMobile(
              steps: controller.steps,
              currentStep: state.currentStep,
              onStepClick: (i) => controller.goToStep(i),
            ),
            Divider(height: 1, color: colors.borderSubtle),
            Expanded(
              child: SingleChildScrollView(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
                child: stepBuilders[state.currentStep](context),
              ),
            ),
            if (state.errorMessage != null &&
                state.status == WizardStatus.failed)
              _FailureBanner(message: state.errorMessage!),
            _Footer(state: state, controller: controller),
          ],
        ),
      ),
    );
  }

  /// Close-button handler. If nothing's been entered, pops immediately;
  /// otherwise surfaces a discard confirmation.
  Future<void> _confirmClose(
    BuildContext context,
    WizardState<TForm> state,
    WizardController<TForm> controller,
  ) async {
    if (state.status == WizardStatus.applied) {
      if (context.mounted) context.pop();
      return;
    }
    final ok = await showConfirmSheet(
      context: context,
      title: 'Discard wizard?',
      message:
          'Form data will be lost. The resource has not been created.',
      confirmLabel: 'Discard',
      danger: true,
    );
    if (ok != true) return;
    if (context.mounted) context.pop();
  }
}

class _Footer<TForm> extends StatelessWidget {
  const _Footer({required this.state, required this.controller});

  final WizardState<TForm> state;
  final WizardController<TForm> controller;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        border: Border(top: BorderSide(color: colors.borderSubtle)),
      ),
      child: SafeArea(
        top: false,
        child: Row(children: _footerButtons(context)),
      ),
    );
  }

  List<Widget> _footerButtons(BuildContext context) {
    switch (state.status) {
      case WizardStatus.applied:
        return [
          const Spacer(),
          FilledButton(
            onPressed: () => context.pop(),
            child: const Text('Done'),
          ),
        ];
      case WizardStatus.applying:
        return [
          const OutlinedButton(onPressed: null, child: Text('Back')),
          const Spacer(),
          const FilledButton(
            onPressed: null,
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: Colors.white,
                  ),
                ),
                SizedBox(width: 8),
                Text('Applying…'),
              ],
            ),
          ),
        ];
      case WizardStatus.previewing:
        return [
          const OutlinedButton(onPressed: null, child: Text('Cancel')),
          const Spacer(),
          const FilledButton(
            onPressed: null,
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: Colors.white,
                  ),
                ),
                SizedBox(width: 8),
                Text('Generating…'),
              ],
            ),
          ),
        ];
      case WizardStatus.reviewing:
        return [
          OutlinedButton(
            onPressed: controller.back,
            child: const Text('Back'),
          ),
          const Spacer(),
          FilledButton(
            onPressed: controller.apply,
            child: const Text('Apply'),
          ),
        ];
      case WizardStatus.failed:
        return [
          OutlinedButton(
            onPressed: () => context.pop(),
            child: const Text('Cancel'),
          ),
          const Spacer(),
          FilledButton(
            onPressed: () {
              if (state.previewYaml != null) {
                controller.apply();
              } else {
                controller.retryPreview();
              }
            },
            child: const Text('Retry'),
          ),
        ];
      case WizardStatus.formEditing:
        final isFirst = state.currentStep == 0;
        return [
          OutlinedButton(
            onPressed:
                isFirst ? () => context.pop() : () => controller.back(),
            child: Text(isFirst ? 'Cancel' : 'Back'),
          ),
          const Spacer(),
          FilledButton(
            onPressed: () => controller.next(),
            child: const Text('Next'),
          ),
        ];
    }
  }
}

class _FailureBanner extends StatelessWidget {
  const _FailureBanner({required this.message});
  final String message;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(16, 10, 16, 10),
      color: colors.error.withValues(alpha: 0.10),
      child: Row(
        children: [
          Icon(Icons.error_outline, color: colors.error, size: 18),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              message,
              style: TextStyle(color: colors.textPrimary, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}
