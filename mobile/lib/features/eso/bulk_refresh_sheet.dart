// ESO Bulk Refresh modal bottom sheet. Walks the eight phases owned by
// `BulkRefreshController` and surfaces snackbars + progress UI matching
// the M4 invariants (drift tri-state coloring, type-to-confirm friction,
// cluster-pin discipline).
//
// Entry point: `showBulkRefreshSheet(context, clusterId)`. The sheet
// stays open across phases — `isDismissible: true` + a top close button
// give the operator two escapes (scrim tap, button) without dropping
// state, since the controller's autoDispose tears the timer down on
// either path.
//
// Type-to-confirm friction is delegated to the shared
// `confirm_sheet.dart` modal (PR-5e-review #3) so this surface stays
// isomorphic with every other destructive write in the app. The preview
// phase renders the byNamespace breakdown + RBAC notice; "Continue"
// transitions preview → confirm and stacks the shared sheet on top.
// Cancel from the inner sheet drops back to preview cleanly.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/eso_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/confirm_sheet.dart';
import '../../widgets/glass_container.dart';
import 'bulk_refresh_controller.dart';
import 'bulk_refresh_scope_picker.dart';

const String kBulkRefreshConfirmToken = 'REFRESH';

/// Show the bulk-refresh sheet. Returns when the operator dismisses it
/// (controller continues poll loop independently when dismissed mid-job).
Future<void> showBulkRefreshSheet({
  required BuildContext context,
  required String clusterId,
}) {
  final colors = Theme.of(context).extension<KubeColors>()!;
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    isDismissible: true,
    backgroundColor: Colors.transparent,
    barrierColor: colors.glassScrim,
    builder: (ctx) => GlassContainer(
      borderRadius: const BorderRadius.vertical(top: Radius.circular(20)),
      child: _BulkRefreshSheet(clusterId: clusterId),
    ),
  );
}

class _BulkRefreshSheet extends ConsumerStatefulWidget {
  const _BulkRefreshSheet({required this.clusterId});

  final String clusterId;

  @override
  ConsumerState<_BulkRefreshSheet> createState() => _BulkRefreshSheetState();
}

class _BulkRefreshSheetState extends ConsumerState<_BulkRefreshSheet> {
  late final AppLifecycleListener _lifecycle;

  @override
  void initState() {
    super.initState();
    // PR-5e-review #9: iOS suspends our 2s periodic Timer when the app
    // backgrounds. Without a resume-time nudge the progress bar can
    // sit stale for up to one full poll interval after the app
    // foregrounds. Fire a one-shot poll on resume; the controller's
    // `_pollInFlight` reentrancy guard makes this safe even when the
    // periodic tick is mid-fetch, and the inner phase guard early-
    // returns when we're not polling.
    _lifecycle = AppLifecycleListener(
      onResume: () {
        if (!mounted) return;
        final ctrl = ref.read(
          bulkRefreshControllerProvider(widget.clusterId).notifier,
        );
        // Fire-and-forget; the controller surfaces any error through
        // its own state machine.
        ctrl.pollNow();
      },
    );
  }

  @override
  void dispose() {
    _lifecycle.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // PR-5e-review #10: watch ONLY the phase here so the outer scaffold
    // (drag handle, header, body switcher) doesn't rebuild on every
    // sub-field tick (e.g., per-poll `job.processed` increments). The
    // deeper field watches are pushed into per-phase Consumer widgets
    // so a 2s `processed` tick rebuilds only the few lines that
    // actually depend on it.
    final phase = ref.watch(bulkRefreshControllerProvider(widget.clusterId)
        .select((s) => s.phase));
    final ctrl =
        ref.read(bulkRefreshControllerProvider(widget.clusterId).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final viewInsets = MediaQuery.of(context).viewInsets;

    return Padding(
      padding: EdgeInsets.only(bottom: viewInsets.bottom),
      child: SafeArea(
        top: false,
        child: ConstrainedBox(
          constraints: BoxConstraints(
            maxHeight: MediaQuery.of(context).size.height * 0.85,
          ),
          child: SingleChildScrollView(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(20, 16, 20, 20),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Center(
                    child: Container(
                      width: 36,
                      height: 4,
                      margin: const EdgeInsets.only(bottom: 12),
                      decoration: BoxDecoration(
                        color: colors.borderSubtle,
                        borderRadius: BorderRadius.circular(2),
                      ),
                    ),
                  ),
                  _Header(phase: phase, colors: colors),
                  const SizedBox(height: 12),
                  _Body(
                      phase: phase,
                      controller: ctrl,
                      clusterId: widget.clusterId),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.phase, required this.colors});

  final BulkRefreshPhase phase;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final label = switch (phase) {
      BulkRefreshPhase.scopePick => 'Refresh ExternalSecrets',
      BulkRefreshPhase.scopeLoad => 'Resolving scope…',
      BulkRefreshPhase.preview => 'Review refresh scope',
      BulkRefreshPhase.confirm => 'Review refresh scope',
      BulkRefreshPhase.submit => 'Starting refresh…',
      BulkRefreshPhase.poll => 'Refreshing…',
      BulkRefreshPhase.done => 'Refresh complete',
      BulkRefreshPhase.error => 'Refresh failed',
    };
    return Text(
      label,
      style: TextStyle(
        color: colors.textPrimary,
        fontSize: 17,
        fontWeight: FontWeight.w600,
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({
    required this.phase,
    required this.controller,
    required this.clusterId,
  });

  /// Bare phase — the only signal that selects which body widget to
  /// mount. Per-phase widgets fetch the rest of state via their own
  /// `Consumer`/`ref.watch(.select)` so a tick on a different slice
  /// doesn't bubble through the body switcher.
  final BulkRefreshPhase phase;
  final BulkRefreshController controller;
  final String clusterId;

  @override
  Widget build(BuildContext context) {
    switch (phase) {
      case BulkRefreshPhase.scopePick:
        return BulkRefreshScopePicker(
          clusterId: clusterId,
          onSubmit: controller.beginScopeLoad,
          onCancel: () => Navigator.of(context).pop(),
        );
      case BulkRefreshPhase.scopeLoad:
        return const _PhaseProgress(
          message: 'Resolving the refresh scope…',
        );
      case BulkRefreshPhase.preview:
      case BulkRefreshPhase.confirm:
        // Preview body stays mounted across both phases. In `confirm`
        // the shared `confirm_sheet.dart` modal is stacked on top; the
        // body underneath still renders the breakdown so a cancel from
        // the inner sheet drops back to a fully-rendered preview rather
        // than a blank flash.
        return _PreviewBody(
          controller: controller,
          clusterId: clusterId,
          continueDisabled: phase == BulkRefreshPhase.confirm,
        );
      case BulkRefreshPhase.submit:
        return const _PhaseProgress(message: 'Sending request…');
      case BulkRefreshPhase.poll:
        return _PollBody(
          clusterId: clusterId,
          onDismiss: () => Navigator.of(context).pop(),
        );
      case BulkRefreshPhase.done:
        return _DoneBody(
          clusterId: clusterId,
          onClose: () => Navigator.of(context).pop(),
        );
      case BulkRefreshPhase.error:
        return _ErrorBody(
          clusterId: clusterId,
          onClose: () => Navigator.of(context).pop(),
          onRetry: controller.backToScopePick,
        );
    }
  }
}

class _PhaseProgress extends StatelessWidget {
  const _PhaseProgress({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Row(
      children: [
        const SizedBox(
          width: 16,
          height: 16,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Text(
            message,
            style: TextStyle(color: colors.textSecondary),
          ),
        ),
      ],
    );
  }
}

/// Renders the resolved-scope breakdown + RBAC notice. The type-to-
/// confirm friction (kBulkRefreshConfirmToken == "REFRESH") is delegated
/// to the shared `showConfirmSheet`; tapping "Continue" advances the
/// controller to `confirm`, stacks the shared sheet on top, and routes
/// the operator's answer back into the controller (submit / backToPreview).
class _PreviewBody extends ConsumerWidget {
  const _PreviewBody({
    required this.controller,
    required this.clusterId,
    required this.continueDisabled,
  });

  final BulkRefreshController controller;
  final String clusterId;

  /// True while the shared confirm sheet is layered on top (phase ==
  /// confirm). Keeps "Continue" disabled so a stray tap on the underlying
  /// surface doesn't double-fire confirmPreview() while the inner sheet
  /// is owning the operator's focus.
  final bool continueDisabled;

  Future<void> _onContinue(
    BuildContext context,
    BulkScopeResponse scope,
    BulkRefreshScope? selectedScope,
  ) async {
    if (scope.visibleCount == 0) return;
    controller.confirmPreview();
    final scopeIdentifier = selectedScope?.displayId() ?? scope.scopeTarget;
    final visibleCount = scope.visibleCount;
    final confirmed = await showConfirmSheet(
      context: context,
      title: 'Refresh $visibleCount ExternalSecret'
          '${visibleCount == 1 ? "" : "s"}',
      message:
          'This triggers an immediate sync of every ExternalSecret in '
          '$scopeIdentifier against its store. Existing Secrets are '
          'overwritten with the upstream value.',
      confirmLabel: 'Refresh $visibleCount',
      typeToConfirm: kBulkRefreshConfirmToken,
      danger: true,
    );
    if (confirmed == true) {
      await controller.submit();
    } else {
      // Cancel / scrim dismiss / null — drop back to preview so the
      // operator can re-review or hit Back.
      controller.backToPreview();
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    // Watch only the slice we depend on (scopeResponse, scope,
    // errorMessage). Phase ticks are owned by the outer sheet.
    final scope = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.scopeResponse));
    final selectedScope = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.scope));
    final errorMessage = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.errorMessage));
    if (scope == null) {
      return Text(
        'Scope not loaded — go back and re-pick.',
        style: TextStyle(color: colors.error, fontSize: 13),
      );
    }
    if (scope.visibleCount == 0) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            "No ExternalSecrets are in scope, or you don't have permission "
            'to refresh them.',
            style: TextStyle(color: colors.textSecondary, fontSize: 13),
          ),
          const SizedBox(height: 20),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              TextButton(
                onPressed: controller.backToScopePick,
                child: const Text('Back'),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('Close'),
              ),
            ],
          ),
        ],
      );
    }

    final scopeIdentifier = selectedScope?.displayId() ?? scope.scopeTarget;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        RichText(
          text: TextSpan(
            style: TextStyle(color: colors.textPrimary, fontSize: 14),
            children: [
              TextSpan(
                text: '${scope.visibleCount}',
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
              TextSpan(
                text: scope.visibleCount == 1
                    ? ' ExternalSecret across '
                    : ' ExternalSecrets across ',
              ),
              TextSpan(
                text: '${scope.byNamespace.length}',
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
              TextSpan(
                text: scope.byNamespace.length == 1
                    ? ' namespace will receive a force-sync from '
                    : ' namespaces will receive a force-sync from ',
              ),
              TextSpan(
                text: scopeIdentifier,
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
              const TextSpan(text: '.'),
            ],
          ),
        ),
        if (scope.restricted) ...[
          const SizedBox(height: 10),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
            decoration: BoxDecoration(
              color: colors.bgElevated,
              borderRadius: BorderRadius.circular(6),
              border: Border.all(color: colors.borderSubtle),
            ),
            child: Text(
              'Showing only resources you can refresh. '
              '${scope.totalCount - scope.visibleCount} additional '
              '${scope.totalCount - scope.visibleCount == 1 ? "is" : "are"} '
              'out of your visibility.',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
        ],
        const SizedBox(height: 12),
        Container(
          constraints: const BoxConstraints(maxHeight: 180),
          decoration: BoxDecoration(
            border: Border.all(color: colors.borderSubtle),
            borderRadius: BorderRadius.circular(6),
          ),
          child: ListView(
            shrinkWrap: true,
            children: [
              for (final row in scope.byNamespace)
                Padding(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 12, vertical: 6),
                  child: Row(
                    children: [
                      Expanded(
                        child: Text(
                          row.namespace,
                          style: TextStyle(
                            color: colors.textPrimary,
                            fontSize: 13,
                            fontFamily: 'monospace',
                          ),
                        ),
                      ),
                      Text(
                        '${row.count}',
                        style: TextStyle(
                          color: colors.textMuted,
                          fontSize: 13,
                        ),
                      ),
                    ],
                  ),
                ),
            ],
          ),
        ),
        if (errorMessage != null) ...[
          const SizedBox(height: 10),
          Text(
            errorMessage,
            style: TextStyle(color: colors.warning, fontSize: 12),
          ),
        ],
        const SizedBox(height: 16),
        Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            TextButton(
              onPressed:
                  continueDisabled ? null : controller.backToScopePick,
              style: TextButton.styleFrom(
                foregroundColor: colors.textSecondary,
              ),
              child: const Text('Back'),
            ),
            const SizedBox(width: 8),
            FilledButton(
              onPressed: continueDisabled
                  ? null
                  : () => _onContinue(context, scope, selectedScope),
              style: FilledButton.styleFrom(
                backgroundColor: colors.accent,
                foregroundColor: Colors.white,
              ),
              child: const Text('Continue'),
            ),
          ],
        ),
      ],
    );
  }
}

class _PollBody extends ConsumerWidget {
  const _PollBody({required this.clusterId, required this.onDismiss});

  final String clusterId;
  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    // Watch only the fields we render. Per the PR-5e-review #10 rebuild
    // pattern, the outer sheet's phase-only watch means this widget is
    // the only thing that re-builds on per-poll job ticks.
    final job = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.job));
    final jobId = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.jobId));
    final attached = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.attachedToExistingJob));
    final pollRetrying = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.pollRetrying));
    final takingLong = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.takingLong));
    final targetCount = job?.targetCount ?? 0;
    final processed = job?.processed ?? 0;
    final progressValue = (job != null && targetCount > 0)
        ? (processed / targetCount).clamp(0.0, 1.0)
        : null;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (attached)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: Text(
              'Attached to an existing refresh job — counts come from the '
              'in-flight job, not a fresh resolution.',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
        LinearProgressIndicator(
          value: progressValue,
          minHeight: 6,
          backgroundColor: colors.bgElevated,
        ),
        const SizedBox(height: 8),
        // Live-region semantics so VoiceOver/TalkBack announce count
        // updates without the operator needing to re-focus the field.
        Semantics(
          liveRegion: true,
          child: Text(
            job == null
                ? 'Tracking job ${jobId ?? ""}…'
                : 'Refreshing $processed of $targetCount',
            style: TextStyle(color: colors.textSecondary, fontSize: 13),
          ),
        ),
        if (pollRetrying) ...[
          const SizedBox(height: 6),
          Text(
            'Retrying…',
            style: TextStyle(color: colors.warning, fontSize: 12),
          ),
        ],
        if (takingLong) ...[
          const SizedBox(height: 6),
          Text(
            'Taking longer than expected — feel free to dismiss; the job '
            'continues in the background.',
            style: TextStyle(color: colors.warning, fontSize: 12),
          ),
        ],
        if (job != null) ...[
          const SizedBox(height: 12),
          Row(
            children: [
              _StatCell(
                label: 'Succeeded',
                value: '${job.succeeded.length}',
                color: colors.success,
              ),
              const SizedBox(width: 16),
              _StatCell(
                label: 'Failed',
                value: '${job.failed.length}',
                color: colors.error,
              ),
              const SizedBox(width: 16),
              _StatCell(
                label: 'Skipped',
                value: '${job.skipped.length}',
                color: colors.warning,
              ),
            ],
          ),
        ],
        const SizedBox(height: 20),
        Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            FilledButton.tonal(
              onPressed: onDismiss,
              child: const Text('Run in background'),
            ),
          ],
        ),
      ],
    );
  }
}

class _DoneBody extends ConsumerWidget {
  const _DoneBody({required this.clusterId, required this.onClose});

  final String clusterId;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    // PR-5e-review #10: Done is terminal so this watch normally fires
    // once on phase transition. The .select keeps things consistent
    // with the rest of the sheet so a delayed late-arriving poll
    // tick (race against teardown) doesn't bubble through the wrapper.
    final job = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.job));
    if (job == null) {
      // Defensive: done with no job should never happen, but render
      // something rather than throwing.
      return Column(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Text(
            'Refresh complete.',
            style: TextStyle(color: colors.textSecondary, fontSize: 13),
          ),
          const SizedBox(height: 12),
          FilledButton(onPressed: onClose, child: const Text('Close')),
        ],
      );
    }
    final hasFailures = job.failed.isNotEmpty;
    // All-skipped (or all-failed) outcomes are NOT success. Surfacing
    // "All 0 ExternalSecrets were refreshed" in green success copy is
    // an operator-visible lie — they pressed Refresh, ESO touched
    // nothing, and the sheet implied success. Treat any run that did
    // not refresh at least one ES as a warning-tier outcome.
    final hasSuccesses = job.succeeded.isNotEmpty;
    final isWarning = hasFailures || !hasSuccesses;
    final String headline;
    if (hasFailures) {
      headline = 'Refresh finished with ${job.failed.length} failure'
          '${job.failed.length == 1 ? "" : "s"}.';
    } else if (!hasSuccesses) {
      headline = 'All targets were skipped — no ExternalSecrets refreshed.';
    } else {
      headline = 'All ${job.succeeded.length} ExternalSecret'
          '${job.succeeded.length == 1 ? "" : "s"} were refreshed.';
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          headline,
          style: TextStyle(
            color: isWarning ? colors.warning : colors.success,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 10),
        Row(
          children: [
            _StatCell(
              label: 'Succeeded',
              value: '${job.succeeded.length}',
              color: colors.success,
            ),
            const SizedBox(width: 16),
            _StatCell(
              label: 'Failed',
              value: '${job.failed.length}',
              color: colors.error,
            ),
            const SizedBox(width: 16),
            _StatCell(
              label: 'Skipped',
              value: '${job.skipped.length}',
              color: colors.warning,
            ),
          ],
        ),
        if (hasFailures) ...[
          const SizedBox(height: 12),
          _OutcomeList(label: 'Failed', outcomes: job.failed, color: colors.error),
        ],
        if (job.skipped.isNotEmpty) ...[
          const SizedBox(height: 8),
          _OutcomeList(
              label: 'Skipped',
              outcomes: job.skipped,
              color: colors.warning),
        ],
        const SizedBox(height: 20),
        Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            FilledButton(
              onPressed: onClose,
              child: const Text('Close'),
            ),
          ],
        ),
      ],
    );
  }
}

class _ErrorBody extends ConsumerWidget {
  const _ErrorBody({
    required this.clusterId,
    required this.onClose,
    required this.onRetry,
  });

  final String clusterId;
  final VoidCallback onClose;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final errorMessage = ref.watch(bulkRefreshControllerProvider(clusterId)
        .select((s) => s.errorMessage));
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          errorMessage ?? 'Refresh failed.',
          style: TextStyle(color: colors.error, fontSize: 13, height: 1.4),
        ),
        const SizedBox(height: 20),
        Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            TextButton(
              onPressed: onClose,
              style: TextButton.styleFrom(
                foregroundColor: colors.textSecondary,
              ),
              child: const Text('Close'),
            ),
            const SizedBox(width: 8),
            FilledButton(
              onPressed: onRetry,
              child: const Text('Try again'),
            ),
          ],
        ),
      ],
    );
  }
}

class _StatCell extends StatelessWidget {
  const _StatCell({
    required this.label,
    required this.value,
    required this.color,
  });

  final String label;
  final String value;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          label,
          style: TextStyle(color: colors.textMuted, fontSize: 11),
        ),
        const SizedBox(height: 2),
        Text(
          value,
          style: TextStyle(
            color: color,
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ],
    );
  }
}

/// PR-5e-review #10: large failed/skipped lists (a stuck Vault store
/// dumping thousands of "auth method failed" rows is the worst case)
/// used to render every single row eagerly inside a `Column`, which
/// blew through the sheet's max-height and stalled the scroll. Now caps
/// at [_initialCap] rows and exposes a "Show all (N)" affordance for the
/// tail. The visible rows use `ListView.builder` so even an expanded
/// 10k-row list paints lazily.
class _OutcomeList extends StatefulWidget {
  const _OutcomeList({
    required this.label,
    required this.outcomes,
    required this.color,
  });

  final String label;
  final List<BulkRefreshOutcome> outcomes;
  final Color color;

  /// Max rows to render before the operator opts into the full list.
  /// Picked as 50 because it covers the top end of "looks like a real
  /// list" without overflowing a 0.85-screen modal even on small phones.
  static const int _initialCap = 50;

  @override
  State<_OutcomeList> createState() => _OutcomeListState();
}

class _OutcomeListState extends State<_OutcomeList> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final total = widget.outcomes.length;
    final capped = total > _OutcomeList._initialCap && !_expanded;
    final visible = capped
        ? widget.outcomes.sublist(0, _OutcomeList._initialCap)
        : widget.outcomes;
    return ExpansionTile(
      tilePadding: EdgeInsets.zero,
      childrenPadding: EdgeInsets.zero,
      title: Text(
        '${widget.label} ($total)',
        style: TextStyle(color: colors.textSecondary, fontSize: 13),
      ),
      children: [
        Container(
          decoration: BoxDecoration(
            border: Border.all(color: colors.borderSubtle),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Column(
            children: [
              // Constrained-height ListView.builder so even the expanded
              // 10k-row case paints lazily and stays scrollable.
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 240),
                child: ListView.builder(
                  shrinkWrap: true,
                  itemCount: visible.length,
                  itemBuilder: (context, i) => _OutcomeRow(
                    outcome: visible[i],
                    color: widget.color,
                    colors: colors,
                  ),
                ),
              ),
              if (capped)
                Padding(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 6, vertical: 4),
                  child: Row(
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: [
                      TextButton(
                        onPressed: () => setState(() => _expanded = true),
                        child: Text('Show all ($total)'),
                      ),
                    ],
                  ),
                ),
            ],
          ),
        ),
      ],
    );
  }
}

/// One row in `_OutcomeList`. Extracted so widget-tests can count the
/// rendered row count via `find.byType(_OutcomeRow)`.
class _OutcomeRow extends StatelessWidget {
  const _OutcomeRow({
    required this.outcome,
    required this.color,
    required this.colors,
  });

  final BulkRefreshOutcome outcome;
  final Color color;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      child: Row(
        children: [
          Expanded(
            child: Text(
              outcome.uid,
              style: TextStyle(
                color: colors.textPrimary,
                fontFamily: 'monospace',
                fontSize: 11,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          const SizedBox(width: 8),
          Text(
            outcome.reason,
            style: TextStyle(color: color, fontSize: 11),
          ),
        ],
      ),
    );
  }
}
