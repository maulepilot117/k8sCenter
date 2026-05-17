// ESO Bulk Refresh modal bottom sheet. Walks the four phases owned by
// `BulkRefreshController` and surfaces snackbars + progress UI matching
// the M4 invariants (drift tri-state coloring, type-to-confirm friction,
// cluster-pin discipline).
//
// Entry point: `showBulkRefreshSheet(context, clusterId)`. The sheet
// stays open across phases — `isDismissible: true` + a top close button
// give the operator two escapes (scrim tap, button) without dropping
// state, since the controller's autoDispose tears the timer down on
// either path.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/eso_repository.dart';
import '../../theme/kube_theme_builder.dart';
import 'bulk_refresh_controller.dart';
import 'bulk_refresh_scope_picker.dart';

const String kBulkRefreshConfirmToken = 'REFRESH';

/// Show the bulk-refresh sheet. Returns when the operator dismisses it
/// (controller continues poll loop independently when dismissed mid-job).
Future<void> showBulkRefreshSheet({
  required BuildContext context,
  required String clusterId,
}) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    isDismissible: true,
    builder: (ctx) => _BulkRefreshSheet(clusterId: clusterId),
  );
}

class _BulkRefreshSheet extends ConsumerWidget {
  const _BulkRefreshSheet({required this.clusterId});

  final String clusterId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(bulkRefreshControllerProvider(clusterId));
    final ctrl = ref.read(bulkRefreshControllerProvider(clusterId).notifier);
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
                  _Header(phase: state.phase, colors: colors),
                  const SizedBox(height: 12),
                  _Body(state: state, controller: ctrl, clusterId: clusterId),
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
      BulkRefreshPhase.confirm => 'Confirm refresh',
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

class _Body extends ConsumerWidget {
  const _Body({
    required this.state,
    required this.controller,
    required this.clusterId,
  });

  final BulkRefreshSheetState state;
  final BulkRefreshController controller;
  final String clusterId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    switch (state.phase) {
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
      case BulkRefreshPhase.confirm:
        return _ConfirmBody(
          state: state,
          onConfirm: controller.submit,
          onBack: controller.backToScopePick,
          onCancel: () => Navigator.of(context).pop(),
        );
      case BulkRefreshPhase.submit:
        return const _PhaseProgress(message: 'Sending request…');
      case BulkRefreshPhase.poll:
        return _PollBody(
          state: state,
          onDismiss: () => Navigator.of(context).pop(),
        );
      case BulkRefreshPhase.done:
        return _DoneBody(
          state: state,
          onClose: () => Navigator.of(context).pop(),
        );
      case BulkRefreshPhase.error:
        return _ErrorBody(
          state: state,
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

class _ConfirmBody extends StatefulWidget {
  const _ConfirmBody({
    required this.state,
    required this.onConfirm,
    required this.onBack,
    required this.onCancel,
  });

  final BulkRefreshSheetState state;
  final VoidCallback onConfirm;
  final VoidCallback onBack;
  final VoidCallback onCancel;

  @override
  State<_ConfirmBody> createState() => _ConfirmBodyState();
}

class _ConfirmBodyState extends State<_ConfirmBody> {
  late final TextEditingController _input = TextEditingController()
    ..addListener(() => setState(() {}));

  @override
  void dispose() {
    _input.dispose();
    super.dispose();
  }

  bool get _canConfirm {
    final scope = widget.state.scopeResponse;
    if (scope == null || scope.visibleCount == 0) return false;
    return _input.text.trim() == kBulkRefreshConfirmToken;
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final scope = widget.state.scopeResponse;
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
                onPressed: widget.onBack,
                child: const Text('Back'),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: widget.onCancel,
                child: const Text('Close'),
              ),
            ],
          ),
        ],
      );
    }

    final scopeIdentifier =
        widget.state.scope?.displayId() ?? scope.scopeTarget;

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
        const SizedBox(height: 16),
        Text.rich(
          TextSpan(
            style: TextStyle(color: colors.textSecondary, fontSize: 13),
            children: [
              const TextSpan(text: 'Type '),
              TextSpan(
                text: kBulkRefreshConfirmToken,
                style: const TextStyle(
                  fontFamily: 'monospace',
                  fontWeight: FontWeight.w600,
                ),
              ),
              const TextSpan(text: ' to confirm'),
            ],
          ),
        ),
        const SizedBox(height: 8),
        TextField(
          controller: _input,
          autofocus: true,
          autocorrect: false,
          enableSuggestions: false,
          textCapitalization: TextCapitalization.characters,
          autofillHints: const <String>[],
          style: TextStyle(
            fontFamily: 'monospace',
            color: colors.textPrimary,
          ),
          decoration: InputDecoration(
            hintText: kBulkRefreshConfirmToken,
            hintStyle: TextStyle(
              color: colors.textMuted,
              fontFamily: 'monospace',
            ),
            border: const OutlineInputBorder(),
          ),
        ),
        if (widget.state.errorMessage != null) ...[
          const SizedBox(height: 10),
          Text(
            widget.state.errorMessage!,
            style: TextStyle(color: colors.warning, fontSize: 12),
          ),
        ],
        const SizedBox(height: 16),
        Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            TextButton(
              onPressed: widget.onBack,
              style: TextButton.styleFrom(
                foregroundColor: colors.textSecondary,
              ),
              child: const Text('Back'),
            ),
            const SizedBox(width: 8),
            FilledButton(
              onPressed: _canConfirm ? widget.onConfirm : null,
              style: FilledButton.styleFrom(
                backgroundColor: colors.accent,
                foregroundColor: Colors.white,
              ),
              child: Text('Refresh ${scope.visibleCount}'),
            ),
          ],
        ),
      ],
    );
  }
}

class _PollBody extends StatelessWidget {
  const _PollBody({required this.state, required this.onDismiss});

  final BulkRefreshSheetState state;
  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final job = state.job;
    final targetCount = job?.targetCount ?? 0;
    final processed = job?.processed ?? 0;
    final progressValue = (job != null && targetCount > 0)
        ? (processed / targetCount).clamp(0.0, 1.0)
        : null;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (state.attachedToExistingJob)
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
                ? 'Tracking job ${state.jobId ?? ""}…'
                : 'Refreshing $processed of $targetCount',
            style: TextStyle(color: colors.textSecondary, fontSize: 13),
          ),
        ),
        if (state.pollRetrying) ...[
          const SizedBox(height: 6),
          Text(
            'Retrying…',
            style: TextStyle(color: colors.warning, fontSize: 12),
          ),
        ],
        if (state.takingLong) ...[
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

class _DoneBody extends StatelessWidget {
  const _DoneBody({required this.state, required this.onClose});

  final BulkRefreshSheetState state;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final job = state.job;
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
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          hasFailures
              ? 'Refresh finished with ${job.failed.length} failure'
                  '${job.failed.length == 1 ? "" : "s"}.'
              : 'All ${job.succeeded.length} ExternalSecret'
                  '${job.succeeded.length == 1 ? "" : "s"} were refreshed.',
          style: TextStyle(
            color: hasFailures ? colors.warning : colors.success,
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

class _ErrorBody extends StatelessWidget {
  const _ErrorBody({
    required this.state,
    required this.onClose,
    required this.onRetry,
  });

  final BulkRefreshSheetState state;
  final VoidCallback onClose;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          state.errorMessage ?? 'Refresh failed.',
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

class _OutcomeList extends StatelessWidget {
  const _OutcomeList({
    required this.label,
    required this.outcomes,
    required this.color,
  });

  final String label;
  final List<BulkRefreshOutcome> outcomes;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ExpansionTile(
      tilePadding: EdgeInsets.zero,
      childrenPadding: EdgeInsets.zero,
      title: Text(
        '$label (${outcomes.length})',
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
              for (final o in outcomes)
                Padding(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 10, vertical: 6),
                  child: Row(
                    children: [
                      Expanded(
                        child: Text(
                          o.uid,
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
                        o.reason,
                        style: TextStyle(color: color, fontSize: 11),
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
