// Compliance history — admin-only line chart over
// `GET /v1/policies/compliance/history`. Fixed 30-day window in M4 (per
// Scope Boundaries — custom ranges deferred to M5 polish).
//
// 503 distinguished-error path: when the backend responds 503 with body
// "requires a database", that's the ComplianceStore PostgreSQL persistence
// being unconfigured. Surface as a permanent (non-retry-able) empty state
// — pointing the operator at desktop setup rather than offering a Retry
// button that will deterministically fail again. Other 503s (rolling
// restarts, network blips) stay retry-able.

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

class _HistoryBody extends ConsumerWidget {
  const _HistoryBody({required this.clusterId});

  final String clusterId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(
      complianceHistoryProvider(ComplianceHistoryKey(clusterId: clusterId)),
    );

    return async.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => _ErrorOrUnconfigured(
        error: e,
        onRetry: () => ref.invalidate(
          complianceHistoryProvider(ComplianceHistoryKey(clusterId: clusterId)),
        ),
      ),
      data: (points) {
        if (points.isEmpty) {
          return const EmptyState(
            title: 'No compliance snapshots yet',
            message:
                'The compliance store is configured but has not recorded any '
                'snapshots yet. The first datapoint will appear on the next '
                'daily aggregation.',
            icon: Icons.timeline_outlined,
          );
        }
        return _HistoryChart(points: points, colors: colors);
      },
    );
  }
}

/// Routes the error envelope to either the permanent "database not
/// configured" empty state or the retry-able generic error view. The
/// discriminator is the substring match exposed by
/// [isComplianceHistoryNotConfigured] — both forms hit 503 but only one
/// should be retried.
class _ErrorOrUnconfigured extends StatelessWidget {
  const _ErrorOrUnconfigured({required this.error, required this.onRetry});

  final Object error;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    if (isComplianceHistoryNotConfigured(error)) {
      // Permanent empty state — no Retry button. The backend will
      // deterministically return 503 with the same message until
      // PostgreSQL is configured server-side, which the operator cannot
      // fix from the phone.
      return const FeatureUnavailableState(
        featureName: 'Compliance history',
        helpMessage:
            'Compliance history requires database storage on the k8sCenter '
            'backend. Configure PostgreSQL persistence in the Helm values '
            'to enable historical trend tracking.',
        icon: Icons.storage_outlined,
      );
    }
    return ErrorStateView(
      message: error is ApiError ? (error as ApiError).message : error.toString(),
      onRetry: onRetry,
    );
  }
}

class _HistoryChart extends StatelessWidget {
  const _HistoryChart({required this.points, required this.colors});

  final List<ComplianceHistoryPoint> points;
  final KubeColors colors;

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

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
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
        ),
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
