// Detail screen for a single GitOps Application. Composite ID-driven —
// the route segment is `Uri.encodeComponent(app.id)` and is decoded on
// mount via [GitOpsId.tryParse] (PR-4a's composite-id helper).
//
// Tab visibility is derived from the `id` prefix, not the `tool` field,
// because the `tool` field collapses `flux-ks` and `flux-hr` to the
// same `"fluxcd"` value. The backend ID-prefix discriminator is what
// the Flux HelmRelease case needs to hide Resources + History.
//
//   "argo:ns:name"     → Overview, Resources, History, Events
//   "flux-ks:ns:name"  → Overview, Resources, History, Events
//   "flux-hr:ns:name"  → Overview, Events
//
// PR-4j will add async commit enrichment (`/v1/gitops/commits`) on top
// of the History tab. Per the plan's "Deferred to Follow-Up Work"
// section, PR-4e ships the History without commit subjects.
//
// Verb actions (sync/suspend/rollback) are out of scope for PR-4e per
// the plan ("this PR only adds the Detail tab views").

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/gitops_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../util/composite_id.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';

class ApplicationDetailScreen extends ConsumerWidget {
  const ApplicationDetailScreen({super.key, required this.id});

  /// Pre-decoded by the go_router builder — composite tool:ns:name.
  final String id;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final parsed = GitOpsId.tryParse(id);
    if (parsed == null) {
      return Scaffold(
        appBar: AppBar(title: const Text('Application')),
        body: const Center(
          child: Padding(
            padding: EdgeInsets.all(24),
            child: Text(
              'Invalid application ID. Open this surface from the GitOps '
              'applications list.',
              textAlign: TextAlign.center,
            ),
          ),
        ),
      );
    }

    final clusterId = ref.watch(activeClusterProvider);
    final detailAsync = ref.watch(gitOpsApplicationDetailProvider(
      GitOpsAppKey(clusterId: clusterId, id: id),
    ));

    return Scaffold(
      appBar: AppBar(title: Text(parsed.name)),
      body: detailAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: ErrorStateView(
              message: _humanise(e),
              onRetry: () => ref.invalidate(gitOpsApplicationDetailProvider(
                GitOpsAppKey(clusterId: clusterId, id: id),
              )),
            ),
          ),
        ),
        data: (detail) => _DetailBody(detail: detail, namespaceHint: parsed.namespace),
      ),
    );
  }

  String _humanise(Object err) {
    if (err is ApiError) {
      if (err.statusCode == 404) {
        return 'Application $id was not found. It may have been deleted.';
      }
      if (err.statusCode == 403) {
        return 'You don\'t have permission to view this application.';
      }
      return err.message;
    }
    return err.toString();
  }
}

class _DetailBody extends StatelessWidget {
  const _DetailBody({required this.detail, required this.namespaceHint});

  final AppDetail detail;
  final String namespaceHint;

  @override
  Widget build(BuildContext context) {
    final app = detail.app;
    final hidesResourcesHistory = app.hidesResourcesAndHistory;

    // Tab count: Overview + Events always, Resources + History conditional.
    final tabs = <Tab>[
      const Tab(text: 'Overview'),
      if (!hidesResourcesHistory) const Tab(text: 'Resources'),
      if (!hidesResourcesHistory) const Tab(text: 'History'),
      const Tab(text: 'Events'),
    ];
    final views = <Widget>[
      _OverviewTab(app: app),
      if (!hidesResourcesHistory)
        _ResourcesTab(resources: detail.resources ?? const <ManagedResource>[]),
      if (!hidesResourcesHistory)
        _HistoryTab(history: detail.history ?? const <RevisionEntry>[]),
      EventsTab(
        kind: app.kind,
        namespace: app.namespace.isEmpty ? namespaceHint : app.namespace,
        name: app.name,
      ),
    ];

    return DefaultTabController(
      length: tabs.length,
      child: Column(
        children: [
          Material(
            color: Theme.of(context).appBarTheme.backgroundColor,
            child: TabBar(
              isScrollable: true,
              tabs: tabs,
              tabAlignment: TabAlignment.start,
            ),
          ),
          Expanded(child: TabBarView(children: views)),
        ],
      ),
    );
  }
}

class _OverviewTab extends StatelessWidget {
  const _OverviewTab({required this.app});

  final NormalizedApp app;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final rows = <_KvRow>[
      _KvRow('Tool', _toolLabel(app.tool)),
      _KvRow('Kind', app.kind),
      _KvRow('Namespace', app.namespace.isEmpty ? '—' : app.namespace),
      _KvRow('Sync', app.syncStatus, color: _syncColor(colors, app.syncStatus)),
      _KvRow('Health', app.healthStatus,
          color: _healthColor(colors, app.healthStatus)),
      if (app.suspended) const _KvRow('Suspended', 'true'),
      if (app.destinationCluster != null)
        _KvRow('Destination cluster', app.destinationCluster!),
      if (app.destinationNamespace != null)
        _KvRow('Destination namespace', app.destinationNamespace!),
      _KvRow('Managed resources', '${app.managedResourceCount}'),
      if (app.currentRevision != null)
        _KvRow('Current revision', _short(app.currentRevision!)),
      if (app.lastSyncTime != null)
        _KvRow('Last sync', app.lastSyncTime!),
    ];
    final source = app.source;
    final hasSource = source.repoURL != null ||
        source.chartName != null ||
        source.path != null ||
        source.targetRevision != null;

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Application',
                  style: TextStyle(
                    color: colors.textMuted,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    letterSpacing: 0.5,
                  ),
                ),
                const SizedBox(height: 8),
                for (final r in rows) _KvLine(row: r),
              ],
            ),
          ),
        ),
        if (hasSource) ...[
          const SizedBox(height: 12),
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'Source',
                    style: TextStyle(
                      color: colors.textMuted,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      letterSpacing: 0.5,
                    ),
                  ),
                  const SizedBox(height: 8),
                  if (source.repoURL != null)
                    _KvLine(row: _KvRow('Repository', source.repoURL!)),
                  if (source.path != null)
                    _KvLine(row: _KvRow('Path', source.path!)),
                  if (source.targetRevision != null)
                    _KvLine(
                        row: _KvRow('Target revision', source.targetRevision!)),
                  if (source.chartName != null)
                    _KvLine(
                      row: _KvRow(
                        'Chart',
                        source.chartVersion == null
                            ? source.chartName!
                            : '${source.chartName} v${source.chartVersion}',
                      ),
                    ),
                ],
              ),
            ),
          ),
        ],
        if (app.message != null && app.message!.isNotEmpty) ...[
          const SizedBox(height: 12),
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'Message',
                    style: TextStyle(
                      color: colors.textMuted,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      letterSpacing: 0.5,
                    ),
                  ),
                  const SizedBox(height: 8),
                  Text(
                    app.message!,
                    style: TextStyle(color: colors.textSecondary),
                  ),
                ],
              ),
            ),
          ),
        ],
      ],
    );
  }
}

class _ResourcesTab extends StatelessWidget {
  const _ResourcesTab({required this.resources});

  final List<ManagedResource> resources;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    if (resources.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'No managed resources reported. The application may be empty, '
            'or the controller has not reconciled yet.',
            style: TextStyle(color: colors.textMuted),
            textAlign: TextAlign.center,
          ),
        ),
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: resources.length,
      separatorBuilder: (_, _) => Divider(
        color: colors.borderSubtle,
        height: 1,
        indent: 16,
        endIndent: 16,
      ),
      itemBuilder: (context, i) {
        final r = resources[i];
        return Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      r.name,
                      style: TextStyle(
                        color: colors.textPrimary,
                        fontWeight: FontWeight.w600,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  Text(
                    r.kind,
                    style: TextStyle(color: colors.textMuted, fontSize: 12),
                  ),
                ],
              ),
              const SizedBox(height: 4),
              Row(
                children: [
                  if (r.namespace != null)
                    Padding(
                      padding: const EdgeInsets.only(right: 8),
                      child: Text(
                        r.namespace!,
                        style: TextStyle(
                            color: colors.textMuted, fontSize: 11),
                      ),
                    ),
                  if (r.status.isNotEmpty)
                    _StatusPill(
                      label: r.status,
                      color: _syncColor(colors, r.status.toLowerCase()),
                    ),
                  if (r.health != null) ...[
                    const SizedBox(width: 6),
                    _StatusPill(
                      label: r.health!,
                      color: _healthColor(colors, r.health!.toLowerCase()),
                    ),
                  ],
                ],
              ),
            ],
          ),
        );
      },
    );
  }
}

class _HistoryTab extends StatelessWidget {
  const _HistoryTab({required this.history});

  final List<RevisionEntry> history;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    if (history.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'No revision history available.',
            style: TextStyle(color: colors.textMuted),
          ),
        ),
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: history.length,
      separatorBuilder: (_, _) => Divider(
        color: colors.borderSubtle,
        height: 1,
        indent: 16,
        endIndent: 16,
      ),
      itemBuilder: (context, i) {
        final h = history[i];
        return Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      _short(h.revision),
                      style: TextStyle(
                        fontFamily: 'monospace',
                        color: colors.textPrimary,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  _StatusPill(
                    label: h.status,
                    color: _syncColor(colors, h.status.toLowerCase()),
                  ),
                ],
              ),
              if (h.message != null && h.message!.isNotEmpty) ...[
                const SizedBox(height: 4),
                Text(
                  h.message!,
                  style: TextStyle(color: colors.textSecondary, fontSize: 13),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ],
              if (h.deployedAt.isNotEmpty) ...[
                const SizedBox(height: 4),
                Text(
                  'Deployed: ${h.deployedAt}',
                  style: TextStyle(color: colors.textMuted, fontSize: 11),
                ),
              ],
            ],
          ),
        );
      },
    );
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.label, required this.color});

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

class _KvRow {
  const _KvRow(this.key, this.value, {this.color});

  final String key;
  final String value;
  final Color? color;
}

class _KvLine extends StatelessWidget {
  const _KvLine({required this.row});

  final _KvRow row;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 132,
            child: Text(
              row.key,
              style: TextStyle(color: colors.textMuted, fontSize: 13),
            ),
          ),
          Expanded(
            child: Text(
              row.value,
              style: TextStyle(
                color: row.color ?? colors.textPrimary,
                fontSize: 13,
                fontWeight:
                    row.color == null ? FontWeight.w400 : FontWeight.w600,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

String _toolLabel(String tool) => switch (tool) {
      'argocd' => 'Argo CD',
      'fluxcd' => 'Flux',
      'both' => 'Argo CD + Flux',
      '' => '—',
      _ => tool,
    };

String _short(String revision) =>
    revision.length > 8 ? revision.substring(0, 7) : revision;

Color _syncColor(KubeColors colors, String status) {
  return switch (status.toLowerCase()) {
    'synced' => colors.success,
    'outofsync' || 'out of sync' => colors.warning,
    'progressing' => colors.info,
    'stalled' || 'failed' => colors.error,
    _ => colors.textMuted,
  };
}

Color _healthColor(KubeColors colors, String status) {
  return switch (status.toLowerCase()) {
    'healthy' => colors.success,
    'degraded' => colors.error,
    'progressing' => colors.info,
    'suspended' => colors.textMuted,
    _ => colors.textMuted,
  };
}
