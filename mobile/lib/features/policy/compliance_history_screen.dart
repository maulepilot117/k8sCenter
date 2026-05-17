// Compliance history — admin-only line chart over
// `GET /v1/policies/compliance/history`. PR-5f adds a ?days=N preset
// picker (1 / 7 / 30 / 90) above the chart. The picker is the only
// time-range surface here; a custom date-range picker is out of scope
// for M5 because the backend's compliance history endpoint accepts
// `?days=N` only — adding `?start=`/`?end=` is a separate backend
// addendum.
//
// 503 distinguished-error path: when the backend responds 503 with body
// "requires a database", that's the ComplianceStore PostgreSQL persistence
// being unconfigured. Surface as a permanent (non-retry-able) empty state
// — pointing the operator at desktop setup rather than offering a Retry
// button that will deterministically fail again. Other 503s (rolling
// restarts, network blips) stay retry-able. When previous data is
// already on screen, the picker disables and the chart fades to 50%
// during a refresh so transient backend hiccups don't blank the view.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/policy_repository.dart';
import '../../auth/auth_repository.dart';
import '../../auth/auth_state.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/feature_unavailable_state.dart';
import '../../widgets/kube_line_chart.dart';
import 'policy_widgets.dart';

/// Preset day windows accepted by the backend's compliance history
/// endpoint (capped at 90 by `complianceHistory(days:)`).
const List<int> kCompliancePresetDays = [1, 7, 30, 90];

class ComplianceHistoryScreen extends ConsumerWidget {
  const ComplianceHistoryScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final auth = ref.watch(authRepositoryProvider);
    final isAdmin = auth is AuthAuthenticated ? auth.user.isAdmin : false;

    return Scaffold(
      appBar: AppBar(title: const Text('Compliance history')),
      // Admin gate is the first check — non-admin users hit the route via
      // a deep link should see an explicit "admin only" empty state rather
      // than the policy status / database 503 surfaces under it. Backend
      // also enforces via `RequireAdmin` so this is defence-in-depth.
      body: !isAdmin
          ? const EmptyState(
              title: 'Admin only',
              message:
                  'Compliance history is restricted to cluster administrators. '
                  'Switch to an admin account to view trend data.',
              icon: Icons.lock_outline,
            )
          : PolicyStatusGate(
              builder: (clusterId, _) =>
                  _HistoryBody(clusterId: clusterId),
            ),
    );
  }
}

class _HistoryBody extends ConsumerStatefulWidget {
  const _HistoryBody({required this.clusterId});

  final String clusterId;

  @override
  ConsumerState<_HistoryBody> createState() => _HistoryBodyState();
}

class _HistoryBodyState extends ConsumerState<_HistoryBody> {
  int _days = 30;

  // Last successfully fetched datapoints. Holding the previous values
  // across a refresh lets the chart stay visible (faded) while the new
  // ?days=N response loads, and lets us show "Couldn't refresh" with
  // the last-known chart on transient errors instead of blanking to
  // an error state. Cleared on cluster change so cluster A's data
  // never bleeds into cluster B's loading view.
  List<ComplianceHistoryPoint>? _lastSuccess;

  void _setDays(int next) {
    if (next == _days) return;
    setState(() => _days = next);
  }

  @override
  void didUpdateWidget(_HistoryBody oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.clusterId != widget.clusterId) {
      // The State is preserved across cluster switches (PolicyStatusGate
      // rebuilds _HistoryBody with a new clusterId, same widget identity
      // by type position). Without this clear, _lastSuccess would render
      // cluster A's chart under cluster B's spinner during the new key's
      // loading phase.
      _lastSuccess = null;
      _days = 30;
    }
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final key = ComplianceHistoryKey(
      clusterId: widget.clusterId,
      days: _days,
    );
    // Update _lastSuccess outside build() so the assignment stays out
    // of Flutter's pure-build contract. ref.listen is idempotent across
    // rebuilds and fires only on value transitions.
    ref.listen(complianceHistoryProvider(key), (prev, next) {
      next.whenData((points) {
        if (!mounted) return;
        setState(() => _lastSuccess = points);
      });
    });
    final async = ref.watch(complianceHistoryProvider(key));

    return async.when(
      loading: () {
        if (_lastSuccess != null) {
          // Stale-overlay path: keep the previous chart on screen
          // (faded with a spinner) while the new ?days window loads.
          return _HistoryChart(
            points: _lastSuccess!,
            colors: colors,
            days: _days,
            onDaysChanged: _setDays,
            stale: true,
            isLoading: true,
            errorMessage: null,
            onRetry: null,
          );
        }
        return _FreshLoadingView(days: _days);
      },
      error: (e, _) {
        if (isComplianceHistoryNotConfigured(e)) {
          // Permanent empty state — no Retry button, no picker either.
          // The backend deterministically returns 503 with the same
          // message until PostgreSQL is configured server-side, which
          // the operator cannot fix from the phone.
          return const FeatureUnavailableState(
            featureName: 'Compliance history',
            helpMessage:
                'Compliance history requires database storage on the k8sCenter '
                'backend. Configure PostgreSQL persistence in the Helm values '
                'to enable historical trend tracking.',
            icon: Icons.storage_outlined,
          );
        }
        if (_lastSuccess != null) {
          // Stale-overlay path: keep the previous chart with a
          // "Couldn't refresh" banner + Retry CTA.
          return _HistoryChart(
            points: _lastSuccess!,
            colors: colors,
            days: _days,
            onDaysChanged: _setDays,
            stale: true,
            isLoading: false,
            errorMessage: e is ApiError ? e.message : e.toString(),
            onRetry: () => ref.invalidate(complianceHistoryProvider(key)),
          );
        }
        return _FreshErrorView(
          message: e is ApiError ? e.message : e.toString(),
          colors: colors,
          days: _days,
          onRetry: () => ref.invalidate(complianceHistoryProvider(key)),
          onDaysChanged: _setDays,
        );
      },
      data: (points) {
        // _lastSuccess is updated via ref.listen above — no side-effect
        // assignment inside the build pure-function path.
        return _LoadedHistoryView(
          points: points,
          colors: colors,
          days: _days,
          onDaysChanged: _setDays,
        );
      },
    );
  }
}

/// SegmentedButton row over [kCompliancePresetDays]. Pulls its
/// visual treatment from the shared time-range picker (also a
/// `SegmentedButton`) so the picker reads consistently across the
/// app's observability surfaces.
class _DaysPicker extends StatelessWidget {
  const _DaysPicker({
    required this.days,
    required this.enabled,
    required this.onChanged,
  });

  final int days;
  final bool enabled;
  final ValueChanged<int> onChanged;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      child: SegmentedButton<int>(
        segments: const [
          ButtonSegment(value: 1, label: Text('1d')),
          ButtonSegment(value: 7, label: Text('7d')),
          ButtonSegment(value: 30, label: Text('30d')),
          ButtonSegment(value: 90, label: Text('90d')),
        ],
        selected: {days},
        onSelectionChanged: enabled ? (s) => onChanged(s.first) : null,
        showSelectedIcon: false,
        style: const ButtonStyle(
          visualDensity: VisualDensity.compact,
          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        ),
      ),
    );
  }
}

/// First-load loading view — no previous chart to keep visible, so
/// render a centered spinner with the picker disabled.
class _FreshLoadingView extends StatelessWidget {
  const _FreshLoadingView({required this.days});

  final int days;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
          child: _DaysPicker(days: days, enabled: false, onChanged: (_) {}),
        ),
        const Expanded(child: Center(child: CircularProgressIndicator())),
      ],
    );
  }
}

/// First-load error view — no previous chart, so render a full error
/// surface with Retry. The picker stays enabled so the operator can try
/// a shorter window if the larger one is too expensive.
class _FreshErrorView extends StatelessWidget {
  const _FreshErrorView({
    required this.message,
    required this.colors,
    required this.days,
    required this.onRetry,
    required this.onDaysChanged,
  });

  final String message;
  final KubeColors colors;
  final int days;
  final VoidCallback onRetry;
  final ValueChanged<int> onDaysChanged;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
          child: _DaysPicker(
            days: days,
            enabled: true,
            onChanged: onDaysChanged,
          ),
        ),
        Expanded(
          child: ErrorStateView(message: message, onRetry: onRetry),
        ),
      ],
    );
  }
}

/// Steady-state view: backend returned a fresh response. The picker is
/// enabled; the chart renders as the original M4 layout.
class _LoadedHistoryView extends StatelessWidget {
  const _LoadedHistoryView({
    required this.points,
    required this.colors,
    required this.days,
    required this.onDaysChanged,
  });

  final List<ComplianceHistoryPoint> points;
  final KubeColors colors;
  final int days;
  final ValueChanged<int> onDaysChanged;

  @override
  Widget build(BuildContext context) {
    if (points.isEmpty) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
            child: _DaysPicker(
              days: days,
              enabled: true,
              onChanged: onDaysChanged,
            ),
          ),
          const Expanded(
            child: EmptyState(
              title: 'No compliance snapshots yet',
              message:
                  'The compliance store is configured but has not recorded '
                  'any snapshots yet. The first datapoint will appear on '
                  'the next daily aggregation.',
              icon: Icons.timeline_outlined,
            ),
          ),
        ],
      );
    }
    return _HistoryChart(
      points: points,
      colors: colors,
      days: days,
      onDaysChanged: onDaysChanged,
      stale: false,
      isLoading: false,
      errorMessage: null,
      onRetry: null,
    );
  }
}

/// Stale view: a previous response is on screen while a new one loads
/// (fade + spinner overlay) or after a transient error (banner + retry).
/// Keeping the previous chart visible avoids a flash-of-blank when the
/// user picks a different `?days` window or pulls to refresh.
class _HistoryChart extends StatelessWidget {
  const _HistoryChart({
    required this.points,
    required this.colors,
    required this.days,
    required this.onDaysChanged,
    required this.stale,
    required this.isLoading,
    required this.errorMessage,
    required this.onRetry,
  });

  final List<ComplianceHistoryPoint> points;
  final KubeColors colors;
  final int days;
  final ValueChanged<int> onDaysChanged;
  final bool stale;
  final bool isLoading;
  final String? errorMessage;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    // Score series — single line with severity coloured by the latest
    // tier (KubeChartSeverity is per-series, so picking the colour from
    // the most recent datapoint keeps the chart consistent with the
    // dashboard gauge).
    final latest = points.isNotEmpty ? points.last.score : 100.0;
    final tier = complianceScoreTier(latest, colors);
    // Build the chart points and track how many were silently dropped
    // due to unparseable dates. Surfacing the drop count below the
    // chart prevents the operator from misreading a gapped chart as a
    // sparse-data signal when the real cause is malformed backend output.
    final chartPoints = <MetricsPoint>[];
    for (final p in points) {
      final t = _tryParseDate(p.date);
      if (t != null) chartPoints.add((t: t, v: p.score));
    }
    final droppedDates = points.length - chartPoints.length;
    final scoreSeries = (
      label: 'Compliance %',
      points: chartPoints,
      severity: latest >= 90
          ? KubeChartSeverity.success
          : (latest >= 70
              ? KubeChartSeverity.warning
              : KubeChartSeverity.error),
    );

    // While loading, the picker is disabled so the operator can't
    // queue up multiple ?days changes mid-fetch. Errors re-enable
    // the picker — the operator may want to try a smaller window.
    final pickerEnabled = !isLoading;

    final chartCard = Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                'Last ${points.length} ${points.length == 1 ? 'day' : 'days'}',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Spacer(),
              Text(
                'Latest: ${latest.toStringAsFixed(0)}% · ${tier.label}',
                style: TextStyle(
                  color: tier.fg,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          KubeLineChart(series: [scoreSeries], height: 200),
          if (droppedDates > 0)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                droppedDates == 1
                    ? '1 datapoint dropped from the chart due to an '
                        'unparseable date.'
                    : '$droppedDates datapoints dropped from the '
                        'chart due to unparseable dates.',
                style: TextStyle(color: colors.warning, fontSize: 11),
              ),
            ),
        ],
      ),
    );

    final Widget chartWithOverlay = isLoading
        ? Stack(
            children: [
              Opacity(opacity: 0.5, child: chartCard),
              const Positioned.fill(
                child: Center(child: CircularProgressIndicator()),
              ),
            ],
          )
        : chartCard;

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _DaysPicker(
          days: days,
          enabled: pickerEnabled,
          onChanged: onDaysChanged,
        ),
        const SizedBox(height: 12),
        if (errorMessage != null && onRetry != null) ...[
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: colors.warning.withValues(alpha: 0.1),
              borderRadius: BorderRadius.circular(6),
              border: Border.all(color: colors.warning.withValues(alpha: 0.5)),
            ),
            child: MergeSemantics(
              child: Row(
                children: [
                  ExcludeSemantics(
                    child: Icon(Icons.warning_amber_rounded,
                        color: colors.warning, size: 18),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      "Couldn't refresh — showing previous data.",
                      style: TextStyle(
                        color: colors.textPrimary,
                        fontSize: 12,
                      ),
                    ),
                  ),
                  TextButton(
                    onPressed: onRetry,
                    child: const Text('Retry'),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),
        ],
        chartWithOverlay,
        const SizedBox(height: 12),
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: colors.bgSurface,
            borderRadius: BorderRadius.circular(8),
            border: Border.all(color: colors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Daily pass/fail',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 8),
              for (final p in points.reversed.take(7))
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 4),
                  child: Row(
                    children: [
                      SizedBox(
                        width: 88,
                        child: Text(
                          p.date,
                          style: TextStyle(
                            color: colors.textMuted,
                            fontSize: 12,
                            fontFeatures: const [FontFeature.tabularFigures()],
                          ),
                        ),
                      ),
                      SizedBox(
                        width: 56,
                        child: Text(
                          '${p.score.toStringAsFixed(0)}%',
                          style: TextStyle(
                            color: colors.textPrimary,
                            fontSize: 12,
                            fontWeight: FontWeight.w600,
                            fontFeatures: const [FontFeature.tabularFigures()],
                          ),
                        ),
                      ),
                      Expanded(
                        child: Text(
                          '${p.pass} pass · ${p.fail} fail · ${p.warn} warn',
                          style: TextStyle(
                            color: colors.textSecondary,
                            fontSize: 12,
                          ),
                          overflow: TextOverflow.ellipsis,
                        ),
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

/// Parses a `yyyy-MM-dd` string into UTC midnight. Returns null on
/// malformed input rather than throwing, so a single corrupt datapoint
/// from the backend doesn't blank the entire chart.
///
/// `DateTime.parse('2026-05-01')` without a timezone suffix yields a
/// local-time DateTime — on a device in UTC+8 the point nominally for
/// `2026-05-01` becomes 2026-04-30T16:00Z, misaligning the chart axis
/// against the label row. Anchoring with a `T00:00:00Z` suffix forces UTC.
DateTime? _tryParseDate(String s) {
  try {
    return DateTime.parse('${s}T00:00:00Z');
  } on FormatException {
    return null;
  }
}
