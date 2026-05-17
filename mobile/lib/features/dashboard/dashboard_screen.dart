// Dashboard — summary cards driven by /v1/cluster/dashboard-summary.
//
// Phone (< 768px): 2-column card grid.
// Tablet (≥ 768px): 4-column card grid.
//
// Mirrors the web's responsive behavior from Phase 6B. Cards show
// nodes (ready/total), pods (running/pending/failed), services,
// alerts (active/critical), and CPU / Memory utilisation when
// Prometheus metrics are available.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import 'dashboard_repository.dart';
import 'dashboard_state.dart';

class DashboardScreen extends ConsumerStatefulWidget {
  const DashboardScreen({super.key});

  @override
  ConsumerState<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends ConsumerState<DashboardScreen> {
  Future<void>? _inFlightRefresh;

  /// Dedupes rapid pull-to-refresh gestures: a second pull during a
  /// running refresh awaits the same future instead of stacking a fresh
  /// HTTP request.
  Future<void> _onRefresh() {
    final existing = _inFlightRefresh;
    if (existing != null) return existing;
    final fresh = ref.refresh(dashboardSummaryProvider.future).whenComplete(() {
      _inFlightRefresh = null;
    });
    _inFlightRefresh = fresh;
    return fresh;
  }

  @override
  Widget build(BuildContext context) {
    final summaryAsync = ref.watch(dashboardSummaryProvider);

    return RefreshIndicator(
      onRefresh: _onRefresh,
      child: summaryAsync.when(
        data: (summary) => _DashboardGrid(summary: summary),
        loading: () => ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: const [
            SizedBox(height: 200, child: LoadingState()),
          ],
        ),
        error: (e, _) {
          if (e is DashboardLocalOnlyError) {
            return _DashboardLocalOnlyView(
              onSwitchToLocal: () =>
                  // Reset to local cluster — invalidating active cluster
                  // cascades through the FutureProvider.autoDispose chain
                  // and refetches against /v1/cluster/dashboard-summary.
                  ref
                      .read(activeClusterProvider.notifier)
                      .setCluster('local'),
            );
          }
          return ListView(
            physics: const AlwaysScrollableScrollPhysics(),
            children: [
              SizedBox(
                height: 200,
                child: ErrorStateView(
                  message: e.toString(),
                  onRetry: () => ref.invalidate(dashboardSummaryProvider),
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}

class _DashboardLocalOnlyView extends StatelessWidget {
  const _DashboardLocalOnlyView({required this.onSwitchToLocal});

  final VoidCallback onSwitchToLocal;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(24),
      children: [
        SizedBox(
          height: 240,
          child: Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                ExcludeSemantics(
                  child: Icon(Icons.cloud_off, size: 48, color: colors.textMuted),
                ),
                const SizedBox(height: 16),
                Text(
                  'Dashboard summary is local-cluster only',
                  textAlign: TextAlign.center,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 18,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 8),
                Text(
                  'Aggregated counts come from the local cluster\'s informer cache. '
                  'Browse remote clusters via the resource list (PR-1d).',
                  textAlign: TextAlign.center,
                  style: TextStyle(color: colors.textSecondary),
                ),
                const SizedBox(height: 16),
                FilledButton.icon(
                  onPressed: onSwitchToLocal,
                  icon: const Icon(Icons.home_outlined),
                  label: const Text('Switch to local cluster'),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

class _DashboardGrid extends StatelessWidget {
  const _DashboardGrid({required this.summary});

  final DashboardSummary summary;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final crossAxisCount = constraints.maxWidth >= 768 ? 4 : 2;
        final cards = <Widget>[
          _SummaryCard(
            icon: Icons.dns_outlined,
            label: 'Nodes',
            primary: '${summary.nodes.ready}/${summary.nodes.total}',
            secondary: 'ready',
          ),
          _SummaryCard(
            icon: Icons.layers_outlined,
            label: 'Pods',
            primary: '${summary.pods.running}',
            secondary:
                'running · ${summary.pods.pending} pending · ${summary.pods.failed} failed',
          ),
          _SummaryCard(
            icon: Icons.hub_outlined,
            label: 'Services',
            primary: '${summary.servicesTotal}',
            secondary: 'total',
          ),
          _SummaryCard(
            icon: Icons.notifications_active_outlined,
            label: 'Alerts',
            primary: '${summary.alerts.active}',
            secondary: '${summary.alerts.critical} critical',
            highlightSecondary: summary.alerts.critical > 0,
          ),
          if (summary.cpu != null)
            _UtilCard(
              icon: Icons.speed,
              label: 'CPU',
              utilization: summary.cpu!,
            ),
          if (summary.memory != null)
            _UtilCard(
              icon: Icons.memory,
              label: 'Memory',
              utilization: summary.memory!,
            ),
        ];

        return ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: const EdgeInsets.all(16),
          children: [
            GridView.count(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              crossAxisCount: crossAxisCount,
              mainAxisSpacing: 12,
              crossAxisSpacing: 12,
              childAspectRatio: 1.5,
              children: cards,
            ),
          ],
        );
      },
    );
  }
}

class _SummaryCard extends StatelessWidget {
  const _SummaryCard({
    required this.icon,
    required this.label,
    required this.primary,
    required this.secondary,
    this.highlightSecondary = false,
  });

  final IconData icon;
  final String label;
  final String primary;
  final String secondary;
  final bool highlightSecondary;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Semantics(
      container: true,
      label: '$label: $primary, $secondary',
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: colors.bgSurface,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: colors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            MergeSemantics(
              child: Row(
                children: [
                  ExcludeSemantics(
                    child: Icon(icon, size: 18, color: colors.textSecondary),
                  ),
                  const SizedBox(width: 6),
                  Text(
                    label,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 13,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ],
              ),
            ),
            ExcludeSemantics(
              child: Text(
                primary,
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 26,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            ExcludeSemantics(
              child: Text(
                secondary,
                style: TextStyle(
                  color: highlightSecondary ? colors.warning : colors.textMuted,
                  fontSize: 12,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _UtilCard extends StatelessWidget {
  const _UtilCard({
    required this.icon,
    required this.label,
    required this.utilization,
  });

  final IconData icon;
  final String label;
  final Utilization utilization;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;

    if (utilization.unavailable) {
      return _UnavailableCard(icon: icon, label: label, colors: colors);
    }

    // Defend against non-finite percentages from 0/0 PromQL division
    // — `double.nan.clamp(0,100)` returns NaN, which trips
    // LinearProgressIndicator's debug assertion and is undefined in release.
    final raw = utilization.percentage;
    final pct = raw.isFinite ? raw.clamp(0, 100).toDouble() : 0.0;

    final subtitle = StringBuffer('${utilization.used} / ${utilization.total}');
    final reqLim = [
      if (utilization.requests.isNotEmpty) 'req ${utilization.requests}',
      if (utilization.limits.isNotEmpty) 'lim ${utilization.limits}',
    ];
    if (reqLim.isNotEmpty) {
      subtitle.write(' · ${reqLim.join(' · ')}');
    }

    return Semantics(
      container: true,
      label: '$label: ${pct.toStringAsFixed(0)}%, ${subtitle.toString()}',
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: colors.bgSurface,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: colors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            MergeSemantics(
              child: Row(
                children: [
                  ExcludeSemantics(
                    child: Icon(icon, size: 18, color: colors.textSecondary),
                  ),
                  const SizedBox(width: 6),
                  Text(
                    label,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 13,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ],
              ),
            ),
            ExcludeSemantics(
              child: Text(
                '${pct.toStringAsFixed(0)}%',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 26,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            // Progress bar is purely visual — the outer Semantics already
            // announces the percentage in the card label. ExcludeSemantics
            // prevents a doubled read on TalkBack/VoiceOver.
            ExcludeSemantics(
              child: ClipRRect(
                borderRadius: BorderRadius.circular(4),
                child: LinearProgressIndicator(
                  value: pct / 100,
                  backgroundColor: colors.bgElevated,
                  valueColor: AlwaysStoppedAnimation(
                    pct >= 90
                        ? colors.error
                        : pct >= 75
                            ? colors.warning
                            : colors.accent,
                  ),
                  minHeight: 6,
                ),
              ),
            ),
            ExcludeSemantics(
              child: Text(
                subtitle.toString(),
                style: TextStyle(color: colors.textMuted, fontSize: 12),
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Renders when the backend signaled "Prometheus unavailable" via the
/// synthetic `Utilization{Percentage:0, Used:'N/A'}` payload. Distinct
/// from the 0%-but-real-data case so operators don't read green progress
/// bars as healthy when metrics are simply missing.
class _UnavailableCard extends StatelessWidget {
  const _UnavailableCard({
    required this.icon,
    required this.label,
    required this.colors,
  });

  final IconData icon;
  final String label;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      container: true,
      label: '$label: unavailable, Prometheus unavailable',
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: colors.bgSurface,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: colors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            MergeSemantics(
              child: Row(
                children: [
                  ExcludeSemantics(
                    child: Icon(icon, size: 18, color: colors.textSecondary),
                  ),
                  const SizedBox(width: 6),
                  Text(
                    label,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 13,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ],
              ),
            ),
            ExcludeSemantics(
              child: Text(
                '—',
                style: TextStyle(
                  color: colors.textMuted,
                  fontSize: 26,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            MergeSemantics(
              child: Row(
                children: [
                  ExcludeSemantics(
                    child:
                        Icon(Icons.cloud_off, size: 12, color: colors.textMuted),
                  ),
                  const SizedBox(width: 4),
                  Text(
                    'Prometheus unavailable',
                    style: TextStyle(color: colors.textMuted, fontSize: 12),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
