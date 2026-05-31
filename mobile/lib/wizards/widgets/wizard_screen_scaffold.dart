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

import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/confirm_sheet.dart';
import '../wizard_controller.dart';
import '../wizard_registry.dart';
import 'wizard_stepper_mobile.dart';

/// Builder signature for per-step bodies. Each wizard provides one
/// builder per step — for the Review step, the builder typically
/// returns the YAML preview panel.
typedef WizardStepBuilder = Widget Function(BuildContext context);

class WizardScreenScaffold<TForm> extends ConsumerWidget {
  const WizardScreenScaffold({
    super.key,
    required this.wizardType,
    required this.title,
    required this.subtitle,
    required this.wizardKey,
    required this.controllerProvider,
    required this.stepBuilders,
    this.onApplied,
  });

  /// Backend wizard type — used to look up the [WizardEntry] in the
  /// registry so the default [onApplied] can route to the created
  /// resource's detail screen without each wizard duplicating the
  /// same SnackBar + go-to-detail boilerplate.
  final String wizardType;

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

  /// Optional override of the default apply-success handler. The
  /// default reads the [WizardEntry] for [wizardType] and either
  /// navigates to the created resource's detail screen (when the
  /// outcome carries a name + namespace) or back to the resource list.
  /// Most wizards leave this null. Custom flows (e.g. multi-resource
  /// wizards in PR-3c) override.
  final void Function(BuildContext context, WizardApplyOutcome outcome)?
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
        final handler = onApplied ?? _defaultOnApplied;
        handler(context, next.applyOutcome!);
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

  /// Default apply-success handler. Reads the wizard registry to look
  /// up the produced kind and the section path, surfaces a SnackBar
  /// with the created name, then navigates either to the resource's
  /// detail screen (when the outcome carries a namespace) or to the
  /// resource list. Mirrors what each wizard's PR-3a `onApplied` did
  /// individually before this collapse.
  void _defaultOnApplied(BuildContext context, WizardApplyOutcome outcome) {
    final entry = findWizardEntry(wizardType);
    if (entry == null) return;
    final messenger = ScaffoldMessenger.of(context);
    messenger.showSnackBar(
      SnackBar(
        content: Text(outcome.created > 0
            ? 'Created ${entry.label} "${outcome.firstResultName}"'
            : 'Apply complete'),
      ),
    );
    if (!context.mounted) return;
    if (outcome.firstResultName.isNotEmpty &&
        outcome.firstResultNamespace != null) {
      // pushReplacement (not go): swap the just-submitted wizard for the new
      // resource's detail while keeping whatever launched the wizard
      // (dashboard / list) below it, so the detail's back button returns there
      // instead of dead-ending on a stackless route.
      context.pushReplacement(kindDetailPath(
        clusterId: wizardKey.clusterId,
        kind: entry.kind,
        namespace: outcome.firstResultNamespace!,
        name: outcome.firstResultName,
      ));
      return;
    }
    // Fall back to the kind's list under its section, or just home if
    // the section can't be found in domainSections (e.g., wizard for a
    // CRD-discovered kind that doesn't appear in the drawer's static
    // catalogue).
    final section = findDomainSection(entry.kind);
    if (section == null) {
      context.go('/');
      return;
    }
    // pushReplacement so the resource list keeps the launcher below it and its
    // back button works (see the detail branch above).
    context.pushReplacement('/clusters/${wizardKey.clusterId}/'
        '${section.pathSegment}/${entry.kind}');
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
        // Cluster-mismatch is a typed failure — Retry would just re-
        // check the pin and re-fail. Offer Discard & restart so the
        // operator gets back to step 0 instead of a dead-end loop.
        if (state.clusterMismatch) {
          return [
            OutlinedButton(
              onPressed: () => context.pop(),
              child: const Text('Cancel'),
            ),
            const Spacer(),
            FilledButton(
              onPressed: () {
                controller.discardAndReset();
              },
              child: const Text('Discard & restart'),
            ),
          ];
        }
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
