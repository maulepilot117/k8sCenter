// External Secrets Operator dashboard — synced/total hero gauge,
// SyncFailed / Stale / Drifted / Unknown secondary cards, top-N
// failure table. Mirrors `frontend/islands/ESODashboard.tsx`.
//
// Status gating: `esoStatusProvider` gates the surface — when ESO is
// not detected the operator sees `FeatureUnavailableState.eso()` rather
// than an empty dashboard. Tertiary store-breakdown row is deliberately
// minimal at phone size (the desktop dashboard pairs provider + cost
// tier; mobile pushes operators to the per-store metrics panel for
// detailed numbers).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/feature_unavailable_state.dart';
import '../../widgets/kube_gauge_ring.dart';
import '../../widgets/kube_line_chart.dart' show KubeChartSeverity;
import 'eso_widgets.dart';

class EsoDashboardScreen extends ConsumerWidget {
  const EsoDashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(esoStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(title: const Text('External Secrets')),
      body: statusAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ErrorStateView(
          message: e is ApiError ? e.message : e.toString(),
          onRetry: () => ref.invalidate(esoStatusProvider(clusterId)),
        ),
        data: (status) {
          if (!status.detected) return FeatureUnavailableState.eso();
          return _DashboardBody(clusterId: clusterId);
        },
      ),
    );
  }
}

class _DashboardBody extends ConsumerWidget {
  const _DashboardBody({required this.clusterId});

  final String clusterId;

  ExternalSecretListKey get _listKey =>
      ExternalSecretListKey(clusterId: clusterId);

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final esAsync = ref.watch(externalSecretListProvider(_listKey));

    Future<void> handleRefresh() async {
      ref.invalidate(externalSecretListProvider(_listKey));
      ref.invalidate(storesListProvider(clusterId));
      ref.invalidate(clusterStoresListProvider(clusterId));
      try {
        await ref.read(externalSecretListProvider(_listKey).future);
      } on Object {
        // Surfaces via .when error branch.
      }
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: esAsync.when(
        loading: () => const _ScrollableShell(child: LoadingState()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load ExternalSecrets',
          error: e,
          onRetry: handleRefresh,
        ),
        data: (items) => _DashboardContent(
          clusterId: clusterId,
          items: items,
          colors: colors,
        ),
      ),
    );
  }
}

class _DashboardContent extends StatelessWidget {
  const _DashboardContent({
    required this.clusterId,
    required this.items,
    required this.colors,
  });

  final String clusterId;
  final List<ExternalSecret> items;
  final KubeColors colors;

  static const int _failureTableLimit = 50;

  /// Severity ordering for the failure table — mirrors web's
  /// FAILURE_SEVERITY in `ESODashboard.tsx`.
  static int _severityRank(EsoStatus s) => switch (s) {
        EsoStatus.syncFailed => 0,
        EsoStatus.stale => 1,
        EsoStatus.drifted => 2,
        EsoStatus.unknown => 3,
        EsoStatus.refreshing => 4,
        EsoStatus.synced => 5,
      };

  @override
  Widget build(BuildContext context) {
    final total = items.length;
    final counts = _countByStatus(items);
    final synced = counts[EsoStatus.synced] ?? 0;
    final syncFailed = counts[EsoStatus.syncFailed] ?? 0;
    final stale = counts[EsoStatus.stale] ?? 0;
    final drifted = counts[EsoStatus.drifted] ?? 0;
    // Unknown counts ONLY the lifecycle Status — drift Unknown is
    // distinct and shouldn't double-count here. (The web dashboard
    // tracks drift Unknown in the gauge subtitle rather than a card.)
    final unknown = counts[EsoStatus.unknown] ?? 0;

    final pct = total > 0 ? synced / total : 0.0;
    final severity = total == 0
        ? KubeChartSeverity.info
        : (pct >= 0.95
            ? KubeChartSeverity.success
            : (pct >= 0.8
                ? KubeChartSeverity.warning
                : KubeChartSeverity.error));

    final broken = items
        .where((e) =>
            e.status != EsoStatus.synced && e.status != EsoStatus.refreshing)
        .toList()
      ..sort((a, b) {
        final cmp = _severityRank(a.status).compareTo(_severityRank(b.status));
        if (cmp != 0) return cmp;
        final nsCmp = a.namespace.compareTo(b.namespace);
        if (nsCmp != 0) return nsCmp;
        return a.name.compareTo(b.name);
      });

    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 16),
      children: [
        Center(
          child: KubeGaugeRing(
            percentage: pct,
            centerLabel: '$synced / $total',
            subtitle: 'ExternalSecrets synced',
            severity: severity,
            size: 160,
          ),
        ),
        const SizedBox(height: 16),
        GridView.count(
          shrinkWrap: true,
          physics: const NeverScrollableScrollPhysics(),
          crossAxisCount: 2,
          mainAxisSpacing: 8,
          crossAxisSpacing: 8,
          childAspectRatio: 2.4,
          children: [
            _SummaryCard(
              label: 'SyncFailed',
              count: syncFailed,
              color: colors.error,
              colors: colors,
            ),
            _SummaryCard(
              label: 'Stale',
              count: stale,
              color: colors.warning,
              colors: colors,
            ),
            _SummaryCard(
              label: 'Drifted',
              count: drifted,
              color: colors.warning,
              colors: colors,
            ),
            _SummaryCard(
              label: 'Unknown',
              // textMuted, NEVER red. PR-3f learnings #9 in operative
              // form — dashboard summary mirrors the drift pill rule.
              count: unknown,
              color: colors.textMuted,
              colors: colors,
            ),
          ],
        ),
        const SizedBox(height: 16),
        _BrowseLinks(clusterId: clusterId, colors: colors),
        const SizedBox(height: 16),
        if (broken.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 24),
            child: Center(
              child: Text(
                total == 0
                    ? 'No ExternalSecrets in this cluster yet.'
                    : 'Every ExternalSecret is syncing cleanly.',
                style: TextStyle(color: colors.textMuted),
                textAlign: TextAlign.center,
              ),
            ),
          )
        else
          _FailureTable(
            clusterId: clusterId,
            broken: broken.take(_failureTableLimit).toList(),
            colors: colors,
            truncated: broken.length > _failureTableLimit,
          ),
      ],
    );
  }

  static Map<EsoStatus, int> _countByStatus(List<ExternalSecret> items) {
    final out = <EsoStatus, int>{};
    for (final es in items) {
      out[es.status] = (out[es.status] ?? 0) + 1;
    }
    return out;
  }
}

class _SummaryCard extends StatelessWidget {
  const _SummaryCard({
    required this.label,
    required this.count,
    required this.color,
    required this.colors,
  });

  final String label;
  final int count;
  final Color color;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Text(
            label,
            style: TextStyle(
              color: colors.textMuted,
              fontSize: 12,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            '$count',
            style: TextStyle(
              color: color,
              fontSize: 22,
              fontWeight: FontWeight.bold,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
        ],
      ),
    );
  }
}

class _BrowseLinks extends StatelessWidget {
  const _BrowseLinks({required this.clusterId, required this.colors});

  final String clusterId;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    Widget tile(String label, IconData icon, String path) => Expanded(
          child: InkWell(
            onTap: () => context.push(path),
            child: Container(
              padding: const EdgeInsets.symmetric(vertical: 12),
              decoration: BoxDecoration(
                color: colors.bgSurface,
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: colors.borderSubtle),
              ),
              child: Column(
                children: [
                  Icon(icon, color: colors.accent, size: 22),
                  const SizedBox(height: 6),
                  Text(
                    label,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ],
              ),
            ),
          ),
        );

    return Row(
      children: [
        tile('ExternalSecrets', Icons.lock_open_outlined,
            '/clusters/$clusterId/eso/externalsecrets'),
        const SizedBox(width: 8),
        tile('Stores', Icons.account_tree_outlined,
            '/clusters/$clusterId/eso/stores'),
        const SizedBox(width: 8),
        tile('Cluster stores', Icons.public_outlined,
            '/clusters/$clusterId/eso/cluster-stores'),
      ],
    );
  }
}

class _FailureTable extends StatelessWidget {
  const _FailureTable({
    required this.clusterId,
    required this.broken,
    required this.colors,
    required this.truncated,
  });

  final String clusterId;
  final List<ExternalSecret> broken;
  final KubeColors colors;
  final bool truncated;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 4),
            child: Text(
              'Needs attention',
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          for (var i = 0; i < broken.length; i++) ...[
            if (i > 0) Divider(color: colors.borderSubtle, height: 1),
            _FailureRow(
              clusterId: clusterId,
              cert: broken[i],
              colors: colors,
            ),
          ],
          if (truncated)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
              child: Text(
                'Showing the first 50 — open the full list to see more.',
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ),
        ],
      ),
    );
  }
}

class _FailureRow extends StatelessWidget {
  const _FailureRow({
    required this.clusterId,
    required this.cert,
    required this.colors,
  });

  final String clusterId;
  final ExternalSecret cert;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => context.push(
        '/clusters/$clusterId/eso/externalsecrets/'
        '${Uri.encodeComponent(cert.namespace)}/'
        '${Uri.encodeComponent(cert.name)}',
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    cert.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  Text(
                    cert.namespace,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 11,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (cert.readyMessage != null &&
                      cert.readyMessage!.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 2),
                      child: Text(
                        cert.readyMessage!,
                        style: TextStyle(color: colors.textMuted, fontSize: 11),
                        overflow: TextOverflow.ellipsis,
                        maxLines: 2,
                      ),
                    ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            EsoStatusPill(status: cert.status, dense: true),
            const SizedBox(width: 6),
            Icon(Icons.chevron_right, size: 16, color: colors.textMuted),
          ],
        ),
      ),
    );
  }
}

class _ScrollableShell extends StatelessWidget {
  const _ScrollableShell({required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [SizedBox(height: 280, child: child)],
    );
  }
}
