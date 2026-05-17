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

import '../../api/eso_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/kube_gauge_ring.dart';
import '../../widgets/kube_line_chart.dart' show KubeChartSeverity;
import '../../widgets/refresh_guard.dart';
import 'eso_widgets.dart';

class EsoDashboardScreen extends StatelessWidget {
  const EsoDashboardScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('External Secrets'),
        actions: const [
          Padding(
            padding: EdgeInsets.symmetric(horizontal: 8),
            child: Center(child: BulkRefreshButton()),
          ),
        ],
      ),
      body: EsoStatusGate(
        builder: (clusterId) => _DashboardBody(clusterId: clusterId),
      ),
    );
  }
}

class _DashboardBody extends ConsumerStatefulWidget {
  const _DashboardBody({required this.clusterId});

  final String clusterId;

  @override
  ConsumerState<_DashboardBody> createState() => _DashboardBodyState();
}

class _DashboardBodyState extends ConsumerState<_DashboardBody>
    with RefreshGuardMixin {
  ExternalSecretListKey get _listKey =>
      ExternalSecretListKey(clusterId: widget.clusterId);
  StoresListKey get _storesKey => StoresListKey(clusterId: widget.clusterId);

  // Refresh entry point shared by the RefreshIndicator pull-down and the
  // ListErrorShell retry. The guard collapses a second concurrent call
  // into the in-flight future so rapid pulls don't race two
  // invalidate+fetch cycles against the same provider slot.
  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(externalSecretListProvider(_listKey));
        ref.invalidate(storesListProvider(_storesKey));
        ref.invalidate(clusterStoresListProvider(widget.clusterId));
        try {
          // Wait for all three so the RefreshIndicator stays visible
          // until the slowest of them resolves; the ES list is the only
          // one we surface errors for via .when.
          await Future.wait([
            ref.read(externalSecretListProvider(_listKey).future),
            ref.read(storesListProvider(_storesKey).future),
            ref.read(clusterStoresListProvider(widget.clusterId).future),
          ]);
        } on Object {
          // Surfaces via .when error branch.
        }
      });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final esAsync = ref.watch(externalSecretListProvider(_listKey));

    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: esAsync.when(
        loading: () => const _ScrollableShell(child: LoadingState()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load ExternalSecrets',
          error: e,
          onRetry: _handleRefresh,
        ),
        data: (items) => _DashboardContent(
          clusterId: widget.clusterId,
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
    final refreshing = counts[EsoStatus.refreshing] ?? 0;

    // Refreshing is a transient state (30-60s during controller restart
    // / reconcile). Excluding it from the gauge denominator avoids a
    // fleet-wide red dashboard during routine reconciles — the operator
    // sees the actual error rate among items that have a settled state.
    // The hero label still reflects total fleet size; only the
    // percentage and severity colour use the settled subset.
    final settled = total - refreshing;
    final pct = settled > 0 ? synced / settled : 1.0;
    final severity = total == 0 || settled == 0
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
              es: broken[i],
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
    required this.es,
    required this.colors,
  });

  final String clusterId;
  final ExternalSecret es;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => context.push(
        '/clusters/$clusterId/eso/externalsecrets/'
        '${Uri.encodeComponent(es.namespace)}/'
        '${Uri.encodeComponent(es.name)}',
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
                    es.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  Text(
                    es.namespace,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 11,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (es.readyMessage != null &&
                      es.readyMessage!.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 2),
                      child: Text(
                        es.readyMessage!,
                        style: TextStyle(color: colors.textMuted, fontSize: 11),
                        overflow: TextOverflow.ellipsis,
                        maxLines: 2,
                      ),
                    ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            EsoStatusPill(status: es.status, dense: true),
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
