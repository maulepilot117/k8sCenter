// Policy compliance dashboard — KubeGaugeRing hero score + by-engine
// cards (Kyverno + Gatekeeper) + by-severity breakdown + browse tiles to
// the policies + violations + compliance-history surfaces. Mirrors
// `frontend/islands/PolicyDashboard.tsx` and
// `frontend/islands/ComplianceDashboard.tsx` rolled into a single mobile
// screen (the web has two; phone-size doesn't need the split).
//
// Status gating: `PolicyStatusGate` gates the surface — when neither
// engine is detected the operator sees `FeatureUnavailableState.policy()`
// rather than an empty dashboard.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/policy_repository.dart';
import '../../auth/auth_repository.dart';
import '../../auth/auth_state.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/kube_gauge_ring.dart';
import '../../widgets/kube_line_chart.dart' show KubeChartSeverity;
import '../../widgets/refresh_guard.dart';
import 'policy_widgets.dart';

class PolicyDashboardScreen extends StatelessWidget {
  const PolicyDashboardScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Policy & Compliance')),
      body: PolicyStatusGate(
        builder: (clusterId, status) => _DashboardBody(
          clusterId: clusterId,
          status: status,
        ),
      ),
    );
  }
}

class _DashboardBody extends ConsumerStatefulWidget {
  const _DashboardBody({required this.clusterId, required this.status});

  final String clusterId;
  final PolicyDiscoveryStatus status;

  @override
  ConsumerState<_DashboardBody> createState() => _DashboardBodyState();
}

class _DashboardBodyState extends ConsumerState<_DashboardBody>
    with RefreshGuardMixin {
  ComplianceScoreKey get _scoreKey =>
      ComplianceScoreKey(clusterId: widget.clusterId);

  /// Refresh entry point shared by the RefreshIndicator pull-down and the
  /// ListErrorShell retry. The guard collapses concurrent calls into the
  /// in-flight future so rapid pulls don't stack invalidations.
  ///
  /// `policyStatusProvider` is invalidated here so the dashboard reflects
  /// engine-availability changes (engine uninstalled, second engine added)
  /// on pull-to-refresh — without this, the engine cards stay stale until
  /// provider autodispose tears down the cache.
  ///
  /// Partial-failure surfacing: the dashboard's `.when` only watches
  /// `complianceScoreProvider` so a violations or policies fetch failure
  /// would otherwise be silent. Track per-provider failure names and
  /// surface them in a SnackBar so the operator knows the underlying
  /// dataset is stale even though the dashboard rendered cleanly.
  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(policyStatusProvider(widget.clusterId));
        ref.invalidate(complianceScoreProvider(_scoreKey));
        ref.invalidate(policiesListProvider(widget.clusterId));
        ref.invalidate(violationsListProvider(widget.clusterId));
        final fetches = <(String, Future<Object>)>[
          ('status', ref.read(policyStatusProvider(widget.clusterId).future)),
          ('compliance', ref.read(complianceScoreProvider(_scoreKey).future)),
          ('policies', ref.read(policiesListProvider(widget.clusterId).future)),
          ('violations', ref.read(violationsListProvider(widget.clusterId).future)),
        ];
        final failed = <String>[];
        for (final (name, fut) in fetches) {
          try {
            await fut;
          } on Object {
            failed.add(name);
          }
        }
        if (failed.isNotEmpty && mounted) {
          // Compliance score failure already surfaces via the scoreAsync
          // .when branch as a ListErrorShell; if compliance was the only
          // failure, suppress the SnackBar (it would be redundant).
          final extras =
              failed.where((n) => n != 'compliance').toList();
          if (extras.isNotEmpty) {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(
                  'Some data is stale — ${extras.join(', ')} fetch failed.',
                ),
                duration: const Duration(seconds: 4),
              ),
            );
          }
        }
      });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final scoreAsync = ref.watch(complianceScoreProvider(_scoreKey));
    final policiesAsync = ref.watch(policiesListProvider(widget.clusterId));

    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: scoreAsync.when(
        loading: () => const _ScrollableShell(child: LoadingState()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load compliance score',
          error: e,
          onRetry: _handleRefresh,
        ),
        data: (score) => _DashboardContent(
          clusterId: widget.clusterId,
          status: widget.status,
          score: score,
          policiesAsync: policiesAsync,
          colors: colors,
        ),
      ),
    );
  }
}

class _DashboardContent extends ConsumerWidget {
  const _DashboardContent({
    required this.clusterId,
    required this.status,
    required this.score,
    required this.policiesAsync,
    required this.colors,
  });

  final String clusterId;
  final PolicyDiscoveryStatus status;
  final ComplianceScore score;
  final AsyncValue<List<PolicyItem>> policiesAsync;
  final KubeColors colors;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final auth = ref.watch(authRepositoryProvider);
    final isAdmin =
        auth is AuthAuthenticated ? auth.user.isAdmin : false;

    final pctFraction = (score.score / 100).clamp(0.0, 1.0);
    final chartSeverity = score.total == 0
        ? KubeChartSeverity.info
        : (score.score >= 90
            ? KubeChartSeverity.success
            : (score.score >= 70
                ? KubeChartSeverity.warning
                : KubeChartSeverity.error));
    final tier = complianceScoreTier(score.score, colors);

    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 16),
      children: [
        Center(
          child: KubeGaugeRing(
            percentage: pctFraction,
            centerLabel: '${score.score.toStringAsFixed(0)}%',
            subtitle: tier.label,
            severity: chartSeverity,
            size: 160,
          ),
        ),
        const SizedBox(height: 8),
        Center(
          child: Text(
            score.total == 0
                ? 'No policies defined yet.'
                : '${score.pass}/${score.total} policies passing'
                    '${score.warn > 0 ? ' · ${score.warn} audit warn' : ''}',
            style: TextStyle(color: colors.textMuted, fontSize: 13),
            textAlign: TextAlign.center,
          ),
        ),
        const SizedBox(height: 16),
        _EngineCards(status: status, policiesAsync: policiesAsync, colors: colors),
        const SizedBox(height: 16),
        if (score.bySeverity.isNotEmpty)
          _SeverityBreakdown(score: score, colors: colors),
        const SizedBox(height: 16),
        _BrowseLinks(
          clusterId: clusterId,
          isAdmin: isAdmin,
          colors: colors,
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Engine cards (Kyverno + Gatekeeper)
// ---------------------------------------------------------------------------

class _EngineCards extends StatelessWidget {
  const _EngineCards({
    required this.status,
    required this.policiesAsync,
    required this.colors,
  });

  final PolicyDiscoveryStatus status;
  final AsyncValue<List<PolicyItem>> policiesAsync;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    // Per-engine policy counts. When the policies fetch errored or is
    // loading, render the cards with a null count (renders as "—").
    int? kyvernoPolicyCount;
    int? gatekeeperPolicyCount;
    policiesAsync.whenData((policies) {
      kyvernoPolicyCount = policies
          .where((p) => p.engine == PolicyEngine.kyverno)
          .length;
      gatekeeperPolicyCount = policies
          .where((p) => p.engine == PolicyEngine.gatekeeper)
          .length;
    });

    return GridView.count(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      crossAxisCount: 2,
      mainAxisSpacing: 8,
      crossAxisSpacing: 8,
      childAspectRatio: 1.5,
      children: [
        _EngineCard(
          engine: PolicyEngine.kyverno,
          available: status.kyvernoAvailable,
          webhooks: status.kyvernoWebhooks,
          namespace: status.kyvernoNamespace,
          policyCount: kyvernoPolicyCount,
          colors: colors,
        ),
        _EngineCard(
          engine: PolicyEngine.gatekeeper,
          available: status.gatekeeperAvailable,
          webhooks: status.gatekeeperWebhooks,
          namespace: status.gatekeeperNamespace,
          policyCount: gatekeeperPolicyCount,
          colors: colors,
        ),
      ],
    );
  }
}

class _EngineCard extends StatelessWidget {
  const _EngineCard({
    required this.engine,
    required this.available,
    required this.webhooks,
    required this.namespace,
    required this.policyCount,
    required this.colors,
  });

  final PolicyEngine engine;
  final bool available;
  final int webhooks;
  final String? namespace;

  /// Null when the policies fetch hasn't completed.
  final int? policyCount;
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
        children: [
          Row(
            children: [
              EngineBadge(engine: engine, dense: true),
              const Spacer(),
              Icon(
                available ? Icons.check_circle_outline : Icons.cancel_outlined,
                size: 16,
                color: available ? colors.success : colors.textMuted,
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            available ? 'Installed' : 'Not installed',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          if (available) ...[
            const SizedBox(height: 2),
            Text(
              policyCount == null
                  ? '— policies'
                  : '$policyCount ${policyCount == 1 ? 'policy' : 'policies'}',
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
            ),
            Text(
              '$webhooks ${webhooks == 1 ? 'webhook' : 'webhooks'}'
              '${namespace != null ? ' · $namespace' : ''}',
              style: TextStyle(color: colors.textMuted, fontSize: 11),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Severity breakdown
// ---------------------------------------------------------------------------

class _SeverityBreakdown extends StatelessWidget {
  const _SeverityBreakdown({required this.score, required this.colors});

  final ComplianceScore score;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    // Ordered by severity weight desc — critical first. Unknown severities
    // (anything outside kSeverityOrder) are sorted alphabetically at the
    // end to stay deterministic across renders.
    final entries = score.bySeverity.entries.toList()
      ..sort((a, b) {
        final ai = kSeverityOrder.indexOf(a.key);
        final bi = kSeverityOrder.indexOf(b.key);
        if (ai != bi) {
          if (ai == -1) return 1;
          if (bi == -1) return -1;
          return ai.compareTo(bi);
        }
        return a.key.compareTo(b.key);
      });

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'By severity',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 10),
          for (final e in entries) ...[
            _SeverityRow(severity: e.key, counts: e.value, colors: colors),
            const SizedBox(height: 8),
          ],
        ],
      ),
    );
  }
}

class _SeverityRow extends StatelessWidget {
  const _SeverityRow({
    required this.severity,
    required this.counts,
    required this.colors,
  });

  final String severity;
  final PolicySeverityCounts counts;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final fg = policySeverityColor(severity, colors);
    final pct = counts.total > 0 ? counts.pass / counts.total : 1.0;
    return Row(
      children: [
        SizedBox(
          width: 64,
          child: Text(
            policySeverityLabel(severity),
            style: TextStyle(
              color: fg,
              fontSize: 12,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
        Expanded(
          child: ClipRRect(
            borderRadius: BorderRadius.circular(3),
            child: LinearProgressIndicator(
              value: pct,
              minHeight: 6,
              backgroundColor: colors.bgElevated,
              valueColor: AlwaysStoppedAnimation<Color>(fg),
            ),
          ),
        ),
        const SizedBox(width: 8),
        SizedBox(
          width: 72,
          child: Text(
            '${counts.pass}/${counts.total}',
            textAlign: TextAlign.right,
            style: TextStyle(
              color: colors.textMuted,
              fontSize: 11,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Browse links
// ---------------------------------------------------------------------------

class _BrowseLinks extends StatelessWidget {
  const _BrowseLinks({
    required this.clusterId,
    required this.isAdmin,
    required this.colors,
  });

  final String clusterId;
  final bool isAdmin;
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
                    textAlign: TextAlign.center,
                  ),
                ],
              ),
            ),
          ),
        );

    // Compliance history is admin-only — backend route guard returns 403
    // for non-admins. Hiding the tile rather than rendering a disabled card
    // keeps the dashboard from advertising a feature the operator cannot
    // reach. Admins still tap through to the 503-distinguished-error path
    // when the database isn't configured. The spread-conditional omits the
    // tile entirely for non-admins so the remaining two tiles each get 1/2
    // of the row width instead of 1/3 with a phantom flex slot.
    return Row(
      children: [
        tile('Policies', Icons.list_alt_outlined,
            '/clusters/$clusterId/policy/policies'),
        const SizedBox(width: 8),
        tile('Violations', Icons.report_problem_outlined,
            '/clusters/$clusterId/policy/violations'),
        if (isAdmin) ...[
          const SizedBox(width: 8),
          tile('History', Icons.timeline_outlined,
              '/clusters/$clusterId/policy/compliance-history'),
        ],
      ],
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
