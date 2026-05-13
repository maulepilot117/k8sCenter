// Detail screen for a single GitOps Application. Composite ID-driven —
// the route segment is `Uri.encodeComponent(app.id)` and is decoded on
// mount via [GitOpsId.tryParse].
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
// Async commit enrichment (`/v1/gitops/commits`) on the History tab is
// deferred to a follow-up milestone.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/gitops_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../util/composite_id.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';
import 'gitops_widgets.dart';

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
                KvLine(label: 'Tool', value: _toolLabel(app.tool)),
                KvLine(label: 'Kind', value: app.kind),
                KvLine(
                  label: 'Namespace',
                  value: app.namespace.isEmpty ? '—' : app.namespace,
                ),
                KvLine(
                  label: 'Sync',
                  value: app.syncStatus,
                  valueColor: statusColor(colors, app.syncStatus),
                ),
                KvLine(
                  label: 'Health',
                  value: app.healthStatus,
                  valueColor: statusColor(colors, app.healthStatus),
                ),
                if (app.suspended)
                  const KvLine(label: 'Suspended', value: 'true'),
                if (app.destinationCluster != null)
                  KvLine(
                    label: 'Destination cluster',
                    value: app.destinationCluster!,
                  ),
                if (app.destinationNamespace != null)
                  KvLine(
                    label: 'Destination namespace',
                    value: app.destinationNamespace!,
                  ),
                KvLine(
                  label: 'Managed resources',
                  value: '${app.managedResourceCount}',
                ),
                if (app.currentRevision != null)
                  KvLine(
                    label: 'Current revision',
                    value: _short(app.currentRevision!),
                  ),
                if (app.lastSyncTime != null)
                  KvLine(label: 'Last sync', value: app.lastSyncTime!),
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
                    KvLine(label: 'Repository', value: source.repoURL!),
                  if (source.path != null)
                    KvLine(label: 'Path', value: source.path!),
                  if (source.targetRevision != null)
                    KvLine(
                      label: 'Target revision',
                      value: source.targetRevision!,
                    ),
                  if (source.chartName != null)
                    KvLine(
                      label: 'Chart',
                      value: source.chartVersion == null
                          ? source.chartName!
                          : '${source.chartName} v${source.chartVersion}',
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

class _ResourcesTab extends ConsumerWidget {
  const _ResourcesTab({required this.resources});

  final List<ManagedResource> resources;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
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
        // Map CRD kind (PascalCase singular) to the lowercase-plural form
        // used by kindDetailPath / domainSections. Unknown kinds fall
        // through to the generic-detail catch-all route.
        final kindSlug = '${r.kind.toLowerCase()}s';
        return InkWell(
          onTap: () {
            final clusterId = ref.read(activeClusterProvider);
            context.push(
              kindDetailPath(
                clusterId: clusterId,
                kind: kindSlug,
                namespace: r.namespace ?? '',
                name: r.name,
              ),
            );
          },
          child: Padding(
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
                      StatusPill(
                        label: r.status,
                        color: statusColor(colors, r.status),
                      ),
                    if (r.health != null) ...[
                      const SizedBox(width: 6),
                      StatusPill(
                        label: r.health!,
                        color: statusColor(colors, r.health),
                      ),
                    ],
                  ],
                ),
              ],
            ),
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
                  StatusPill(
                    label: h.status,
                    color: statusColor(colors, h.status),
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

String _toolLabel(String tool) => switch (tool) {
      'argocd' => 'Argo CD',
      'fluxcd' => 'Flux',
      'both' => 'Argo CD + Flux',
      '' => '—',
      _ => tool,
    };

/// Truncates only 40-char hex git SHAs to 7 chars. Non-SHA revisions
/// (semver tags, OCI digests) pass through unchanged.
String _short(String revision) {
  if (RegExp(r'^[0-9a-f]{40}$').hasMatch(revision)) {
    return revision.substring(0, 7);
  }
  return revision;
}
