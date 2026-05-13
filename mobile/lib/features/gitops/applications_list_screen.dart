// GitOps Applications list. Top-level surface — reachable from the
// drawer's GitOps section. Mirrors the web `GitOpsApplications.tsx`
// island's filter strategy (tool / sync / health) but compresses the
// chip rows into the mobile width.
//
// Status gating: `gitOpsStatusProvider` decides whether the list
// surface renders or whether the operator sees `FeatureUnavailableState
// .gitops()`. The provider returns `GitOpsStatus.empty` on 5xx so a
// flaky discovery probe doesn't surface as an error card.
//
// Filter UX: three filter chip rows (Tool / Sync / Health). On a phone
// the three rows wrap; on a tablet they sit on one line. The empty
// chip ("All") is the default and is highlighted when active.
//
// Tap a row → `/clusters/<id>/gitops/applications/<encoded-id>` where
// encoded-id is `Uri.encodeComponent(app.id)`. The detail screen
// re-decodes inside its builder.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/gitops_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/feature_unavailable_state.dart';

/// Filter values for the tool chip row. `null` means "show all".
enum _ToolFilter { all, argocd, fluxcd }

/// Filter values for the sync chip row. Backed by the
/// [NormalizedApp.syncStatus] string from the wire.
enum _SyncFilter { all, synced, outofsync, progressing, failed }

/// Filter values for the health chip row.
enum _HealthFilter { all, healthy, degraded, progressing, suspended }

class ApplicationsListScreen extends ConsumerStatefulWidget {
  const ApplicationsListScreen({super.key});

  @override
  ConsumerState<ApplicationsListScreen> createState() =>
      _ApplicationsListScreenState();
}

class _ApplicationsListScreenState
    extends ConsumerState<ApplicationsListScreen> {
  _ToolFilter _tool = _ToolFilter.all;
  _SyncFilter _sync = _SyncFilter.all;
  _HealthFilter _health = _HealthFilter.all;

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(gitOpsStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(
        title: const Text('GitOps Applications'),
      ),
      body: statusAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(e.toString()),
          ),
        ),
        data: (status) {
          if (!status.isInstalled) {
            return FeatureUnavailableState.gitops();
          }
          return _ApplicationsBody(
            clusterId: clusterId,
            tool: _tool,
            sync: _sync,
            health: _health,
            onToolChanged: (v) => setState(() => _tool = v),
            onSyncChanged: (v) => setState(() => _sync = v),
            onHealthChanged: (v) => setState(() => _health = v),
          );
        },
      ),
    );
  }
}

class _ApplicationsBody extends ConsumerWidget {
  const _ApplicationsBody({
    required this.clusterId,
    required this.tool,
    required this.sync,
    required this.health,
    required this.onToolChanged,
    required this.onSyncChanged,
    required this.onHealthChanged,
  });

  final String clusterId;
  final _ToolFilter tool;
  final _SyncFilter sync;
  final _HealthFilter health;
  final ValueChanged<_ToolFilter> onToolChanged;
  final ValueChanged<_SyncFilter> onSyncChanged;
  final ValueChanged<_HealthFilter> onHealthChanged;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(gitOpsApplicationsProvider(clusterId));

    Future<void> handleRefresh() async {
      ref.invalidate(gitOpsApplicationsProvider(clusterId));
      try {
        await ref.read(gitOpsApplicationsProvider(clusterId).future);
      } on Object {
        // surfaces via error branch
      }
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: [
            SizedBox(
              height: 280,
              child: Center(
                child: Padding(
                  padding: const EdgeInsets.all(24),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        'Failed to load applications',
                        style: TextStyle(
                          color: colors.textPrimary,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        e.toString(),
                        style: TextStyle(color: colors.textMuted),
                        textAlign: TextAlign.center,
                      ),
                      const SizedBox(height: 12),
                      OutlinedButton(
                        onPressed: handleRefresh,
                        child: const Text('Retry'),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
        data: (response) {
          final filtered = _applyFilters(response.applications);
          return ListView(
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: [
              _SummaryRow(summary: response.summary),
              _FilterChipsBlock(
                tool: tool,
                sync: sync,
                health: health,
                onToolChanged: onToolChanged,
                onSyncChanged: onSyncChanged,
                onHealthChanged: onHealthChanged,
              ),
              if (filtered.isEmpty)
                Padding(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 16, vertical: 32),
                  child: Center(
                    child: Text(
                      response.applications.isEmpty
                          ? 'No applications. Deploy via Argo CD or Flux to see them here.'
                          : 'No applications match your filters.',
                      style: TextStyle(color: colors.textMuted),
                      textAlign: TextAlign.center,
                    ),
                  ),
                )
              else
                ...filtered.map((app) => _AppRow(
                      app: app,
                      onTap: () => context.push(
                        '/clusters/$clusterId/gitops/applications/'
                        '${Uri.encodeComponent(app.id)}',
                      ),
                    )),
            ],
          );
        },
      ),
    );
  }

  List<NormalizedApp> _applyFilters(List<NormalizedApp> apps) {
    return apps.where((app) {
      if (tool == _ToolFilter.argocd && app.tool != 'argocd') return false;
      if (tool == _ToolFilter.fluxcd && app.tool != 'fluxcd') return false;
      if (sync != _SyncFilter.all && app.syncStatus != sync.name) {
        return false;
      }
      if (health != _HealthFilter.all && app.healthStatus != health.name) {
        return false;
      }
      return true;
    }).toList();
  }
}

class _SummaryRow extends StatelessWidget {
  const _SummaryRow({required this.summary});

  final AppListMetadata summary;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final chips = <_SummaryChipData>[
      _SummaryChipData(label: 'Synced', count: summary.synced, color: colors.success),
      _SummaryChipData(
          label: 'Out of Sync',
          count: summary.outOfSync,
          color: colors.warning),
      _SummaryChipData(
          label: 'Degraded', count: summary.degraded, color: colors.error),
      _SummaryChipData(
          label: 'Progressing',
          count: summary.progressing,
          color: colors.info),
      _SummaryChipData(
          label: 'Suspended',
          count: summary.suspended,
          color: colors.textMuted),
    ];
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        children: chips.map((c) => _SummaryChip(data: c)).toList(),
      ),
    );
  }
}

class _SummaryChipData {
  const _SummaryChipData({
    required this.label,
    required this.count,
    required this.color,
  });

  final String label;
  final int count;
  final Color color;
}

class _SummaryChip extends StatelessWidget {
  const _SummaryChip({required this.data});

  final _SummaryChipData data;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(20),
        color: data.color.withValues(alpha: 0.15),
      ),
      child: Text(
        '${data.count} ${data.label}',
        style: TextStyle(
          color: data.color,
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _FilterChipsBlock extends StatelessWidget {
  const _FilterChipsBlock({
    required this.tool,
    required this.sync,
    required this.health,
    required this.onToolChanged,
    required this.onSyncChanged,
    required this.onHealthChanged,
  });

  final _ToolFilter tool;
  final _SyncFilter sync;
  final _HealthFilter health;
  final ValueChanged<_ToolFilter> onToolChanged;
  final ValueChanged<_SyncFilter> onSyncChanged;
  final ValueChanged<_HealthFilter> onHealthChanged;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _ChipRow<_ToolFilter>(
            label: 'Tool',
            values: _ToolFilter.values,
            selected: tool,
            labelOf: (v) => switch (v) {
              _ToolFilter.all => 'All',
              _ToolFilter.argocd => 'Argo CD',
              _ToolFilter.fluxcd => 'Flux',
            },
            onChanged: onToolChanged,
          ),
          _ChipRow<_SyncFilter>(
            label: 'Sync',
            values: _SyncFilter.values,
            selected: sync,
            labelOf: (v) => switch (v) {
              _SyncFilter.all => 'All',
              _SyncFilter.synced => 'Synced',
              _SyncFilter.outofsync => 'Out of Sync',
              _SyncFilter.progressing => 'Progressing',
              _SyncFilter.failed => 'Failed',
            },
            onChanged: onSyncChanged,
          ),
          _ChipRow<_HealthFilter>(
            label: 'Health',
            values: _HealthFilter.values,
            selected: health,
            labelOf: (v) => switch (v) {
              _HealthFilter.all => 'All',
              _HealthFilter.healthy => 'Healthy',
              _HealthFilter.degraded => 'Degraded',
              _HealthFilter.progressing => 'Progressing',
              _HealthFilter.suspended => 'Suspended',
            },
            onChanged: onHealthChanged,
          ),
        ],
      ),
    );
  }
}

class _ChipRow<T> extends StatelessWidget {
  const _ChipRow({
    required this.label,
    required this.values,
    required this.selected,
    required this.labelOf,
    required this.onChanged,
  });

  final String label;
  final List<T> values;
  final T selected;
  final String Function(T) labelOf;
  final ValueChanged<T> onChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          SizedBox(
            width: 56,
            child: Text(
              label,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
          Expanded(
            child: Wrap(
              spacing: 6,
              runSpacing: 6,
              children: values
                  .map((v) => ChoiceChip(
                        label: Text(labelOf(v)),
                        selected: v == selected,
                        onSelected: (_) => onChanged(v),
                      ))
                  .toList(),
            ),
          ),
        ],
      ),
    );
  }
}

class _AppRow extends StatelessWidget {
  const _AppRow({required this.app, required this.onTap});

  final NormalizedApp app;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final source = app.source.chartName ?? app.source.repoURL;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    app.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontWeight: FontWeight.w600,
                      fontSize: 15,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                _ToolBadge(tool: app.tool),
              ],
            ),
            const SizedBox(height: 4),
            Row(
              children: [
                Text(
                  '${app.kind} · ${app.namespace}',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
                if (app.suspended) ...[
                  const SizedBox(width: 8),
                  _MutedBadge(label: 'Suspended', colors: colors),
                ],
              ],
            ),
            const SizedBox(height: 6),
            Wrap(
              spacing: 6,
              runSpacing: 4,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                _StatusBadge(
                  label: app.syncStatus,
                  color: _syncColor(colors, app.syncStatus),
                ),
                _StatusBadge(
                  label: app.healthStatus,
                  color: _healthColor(colors, app.healthStatus),
                ),
                if (app.managedResourceCount > 0)
                  _MutedBadge(
                    label: '${app.managedResourceCount} resources',
                    colors: colors,
                  ),
              ],
            ),
            if (source != null) ...[
              const SizedBox(height: 4),
              Text(
                source,
                style: TextStyle(color: colors.textSecondary, fontSize: 11),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _ToolBadge extends StatelessWidget {
  const _ToolBadge({required this.tool});

  final String tool;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    if (tool.isEmpty) return const SizedBox.shrink();
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(12),
        color: colors.accent.withValues(alpha: 0.12),
      ),
      child: Text(
        switch (tool) {
          'argocd' => 'Argo CD',
          'fluxcd' => 'Flux',
          'both' => 'Argo+Flux',
          _ => tool,
        },
        style: TextStyle(
          color: colors.accent,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(10),
        color: color.withValues(alpha: 0.15),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _MutedBadge extends StatelessWidget {
  const _MutedBadge({required this.label, required this.colors});

  final String label;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(10),
        color: colors.bgElevated,
      ),
      child: Text(
        label,
        style: TextStyle(
          color: colors.textMuted,
          fontSize: 11,
        ),
      ),
    );
  }
}

Color _syncColor(KubeColors colors, String status) {
  return switch (status) {
    'synced' => colors.success,
    'outofsync' => colors.warning,
    'progressing' => colors.info,
    'stalled' || 'failed' => colors.error,
    _ => colors.textMuted,
  };
}

Color _healthColor(KubeColors colors, String status) {
  return switch (status) {
    'healthy' => colors.success,
    'degraded' => colors.error,
    'progressing' => colors.info,
    'suspended' => colors.textMuted,
    _ => colors.textMuted,
  };
}
