// Dashboard — summary cards driven by /v1/cluster/dashboard-summary.
//
// Phone (< 768px): 2-column card grid.
// Tablet (≥ 768px): 4-column card grid.
//
// Mirrors the web's responsive behavior from Phase 6B. Cards show
// nodes (ready/total), pods (running/pending/failed), services,
// alerts (active/critical), and CPU / Memory utilisation when
// Prometheus metrics are available.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import 'dashboard_repository.dart';
import 'dashboard_state.dart';

class DashboardScreen extends ConsumerWidget {
  const DashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final summaryAsync = ref.watch(dashboardSummaryProvider);

    return RefreshIndicator(
      onRefresh: () => ref.refresh(dashboardSummaryProvider.future),
      child: summaryAsync.when(
        data: (summary) => _DashboardGrid(summary: summary),
        loading: () => const Center(child: LoadingState()),
        error: (e, _) => ErrorStateView(
          message: e.toString(),
          onRetry: () => ref.invalidate(dashboardSummaryProvider),
        ),
      ),
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
    return Container(
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
          Row(
            children: [
              Icon(icon, size: 18, color: colors.textSecondary),
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
          Text(
            primary,
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 26,
              fontWeight: FontWeight.w700,
            ),
          ),
          Text(
            secondary,
            style: TextStyle(
              color: highlightSecondary ? colors.warning : colors.textMuted,
              fontSize: 12,
            ),
          ),
        ],
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
    final pct = utilization.percentage.clamp(0, 100).toDouble();
    return Container(
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
          Row(
            children: [
              Icon(icon, size: 18, color: colors.textSecondary),
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
          Text(
            '${pct.toStringAsFixed(0)}%',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 26,
              fontWeight: FontWeight.w700,
            ),
          ),
          ClipRRect(
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
          Text(
            '${utilization.used} / ${utilization.total}',
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
        ],
      ),
    );
  }
}
