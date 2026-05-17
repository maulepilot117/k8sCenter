// AppSet detail screen. Composite-ID-driven (same `tool:ns:name` shape
// as Applications, with `argo-as` tool prefix). Mirrors the web
// `GitOpsAppSetDetail.tsx` island's panels:
//
//   * Header — name + namespace + status pill
//   * Generators — collapsed-by-default JSON cards (tap to expand)
//   * Template — source + destination
//   * Generated applications — tappable rows linking to the
//     Application detail surface
//   * Conditions — collapsed by default, expandable
//
// AppSet detail uses the tap-to-list-children pattern because child
// Application counts can run into the hundreds.

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/gitops_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../util/composite_id.dart';
import '../../widgets/empty_states.dart';
import 'gitops_widgets.dart';

class ApplicationSetDetailScreen extends ConsumerWidget {
  const ApplicationSetDetailScreen({super.key, required this.id});

  final String id;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final parsed = GitOpsId.tryParse(id);
    if (parsed == null) {
      return Scaffold(
        appBar: AppBar(title: const Text('ApplicationSet')),
        body: const Center(
          child: Padding(
            padding: EdgeInsets.all(24),
            child: Text(
              'Invalid ApplicationSet ID.',
              textAlign: TextAlign.center,
            ),
          ),
        ),
      );
    }
    final clusterId = ref.watch(activeClusterProvider);
    final detailAsync = ref.watch(gitOpsApplicationSetDetailProvider(
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
              onRetry: () => ref.invalidate(gitOpsApplicationSetDetailProvider(
                GitOpsAppKey(clusterId: clusterId, id: id),
              )),
            ),
          ),
        ),
        data: (detail) => _AppSetBody(
          clusterId: clusterId,
          detail: detail,
        ),
      ),
    );
  }

  String _humanise(Object err) {
    if (err is ApiError) {
      if (err.statusCode == 404) {
        return 'ApplicationSet $id was not found. It may have been deleted.';
      }
      if (err.statusCode == 403) {
        return 'You don\'t have permission to view this ApplicationSet.';
      }
      return err.message;
    }
    return err.toString();
  }
}

class _AppSetBody extends ConsumerStatefulWidget {
  const _AppSetBody({required this.clusterId, required this.detail});

  final String clusterId;
  final AppSetDetail detail;

  @override
  ConsumerState<_AppSetBody> createState() => _AppSetBodyState();
}

class _AppSetBodyState extends ConsumerState<_AppSetBody> {
  final Set<int> _expandedGenerators = <int>{};
  bool _conditionsExpanded = false;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final detail = widget.detail;
    final appSet = detail.appSet;
    final apps = detail.applications;
    final src = appSet.templateSource;

    // Build header + generators + template + conditions as a flat list
    // of sliver items; generated applications use SliverList.builder so
    // child tiles don't eagerly mount when counts are large.
    final headerItems = <Widget>[
      Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      appSet.namespace,
                      style:
                          TextStyle(color: colors.textMuted, fontSize: 12),
                    ),
                  ),
                  StatusPill(
                    label: appSet.status.isEmpty ? 'unknown' : appSet.status,
                    color: statusColor(colors, appSet.status),
                  ),
                ],
              ),
              if (appSet.statusMessage != null &&
                  appSet.statusMessage!.isNotEmpty) ...[
                const SizedBox(height: 8),
                Text(
                  appSet.statusMessage!,
                  style:
                      TextStyle(color: colors.textSecondary, fontSize: 13),
                ),
              ],
              const SizedBox(height: 8),
              Text(
                '${appSet.generatedAppCount} generated applications',
                style: TextStyle(color: colors.textSecondary, fontSize: 13),
              ),
            ],
          ),
        ),
      ),
      const SizedBox(height: 12),
      _PanelHeader(
        label: 'Generators (${detail.generators.length})',
        colors: colors,
      ),
      if (detail.generators.isEmpty)
        _EmptyCard(label: 'No generators defined.')
      else
        ...detail.generators.asMap().entries.map((e) {
          final idx = e.key;
          final gen = e.value;
          final type = _detectGeneratorType(gen);
          final isExpanded = _expandedGenerators.contains(idx);
          return Card(
            margin: const EdgeInsets.only(bottom: 8),
            child: Column(
              children: [
                MergeSemantics(
                  child: InkWell(
                    onTap: () => setState(() {
                      if (isExpanded) {
                        _expandedGenerators.remove(idx);
                      } else {
                        _expandedGenerators.add(idx);
                      }
                    }),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 16, vertical: 12),
                      child: Row(
                        children: [
                          Container(
                            padding: const EdgeInsets.symmetric(
                                horizontal: 8, vertical: 2),
                            decoration: BoxDecoration(
                              borderRadius: BorderRadius.circular(10),
                              color: colors.accent.withValues(alpha: 0.12),
                            ),
                            child: Text(
                              type,
                              style: TextStyle(
                                color: colors.accent,
                                fontSize: 11,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          ),
                          const SizedBox(width: 12),
                          Expanded(
                            child: Text(
                              'Generator ${idx + 1}',
                              style: TextStyle(
                                  color: colors.textSecondary, fontSize: 13),
                            ),
                          ),
                          ExcludeSemantics(
                            child: Icon(
                              isExpanded
                                  ? Icons.expand_less
                                  : Icons.expand_more,
                              color: colors.textMuted,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
                if (isExpanded)
                  Padding(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 16, vertical: 8),
                    child: Container(
                      padding: const EdgeInsets.all(12),
                      decoration: BoxDecoration(
                        borderRadius: BorderRadius.circular(6),
                        color: colors.bgSurface,
                      ),
                      child: Text(
                        const JsonEncoder.withIndent('  ').convert(gen),
                        style: TextStyle(
                          fontFamily: 'monospace',
                          color: colors.textSecondary,
                          fontSize: 12,
                        ),
                      ),
                    ),
                  ),
              ],
            ),
          );
        }),
      const SizedBox(height: 12),
      _PanelHeader(label: 'Template', colors: colors),
      Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (src.repoURL != null)
                KvLine(label: 'Repository', value: src.repoURL!),
              if (src.path != null)
                KvLine(label: 'Path', value: src.path!),
              if (src.targetRevision != null)
                KvLine(
                    label: 'Target revision', value: src.targetRevision!),
              if (src.chartName != null)
                KvLine(
                  label: 'Chart',
                  value: src.chartVersion == null
                      ? src.chartName!
                      : '${src.chartName} v${src.chartVersion}',
                ),
              KvLine(
                label: 'Destination',
                value: appSet.templateDestination.isEmpty
                    ? '—'
                    : appSet.templateDestination,
              ),
            ],
          ),
        ),
      ),
      const SizedBox(height: 12),
      _PanelHeader(
        label: 'Generated applications (${apps.length})',
        colors: colors,
      ),
    ];

    final footerItems = <Widget>[
      if (detail.conditions.isNotEmpty) ...[
        const SizedBox(height: 12),
        MergeSemantics(
          child: InkWell(
            onTap: () =>
                setState(() => _conditionsExpanded = !_conditionsExpanded),
            child: Row(
              children: [
                Text(
                  'Conditions (${detail.conditions.length})',
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(width: 8),
                if (detail.conditions.any((c) => c.isError))
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 8, vertical: 2),
                    decoration: BoxDecoration(
                      borderRadius: BorderRadius.circular(10),
                      color: colors.error.withValues(alpha: 0.15),
                    ),
                    child: Text(
                      'errors',
                      style: TextStyle(
                        color: colors.error,
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                const Spacer(),
                ExcludeSemantics(
                  child: Icon(
                    _conditionsExpanded
                        ? Icons.expand_less
                        : Icons.expand_more,
                    color: colors.textMuted,
                  ),
                ),
              ],
            ),
          ),
        ),
        if (_conditionsExpanded) ...[
          const SizedBox(height: 8),
          ...detail.conditions.map((c) => Card(
                margin: const EdgeInsets.only(bottom: 6),
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Expanded(
                            child: Text(
                              c.type,
                              style: TextStyle(
                                color: c.isError
                                    ? colors.error
                                    : colors.textPrimary,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          ),
                          Text(
                            c.status,
                            style: TextStyle(
                              color: c.isError
                                  ? colors.error
                                  : colors.textSecondary,
                              fontSize: 12,
                            ),
                          ),
                        ],
                      ),
                      if (c.message != null && c.message!.isNotEmpty) ...[
                        const SizedBox(height: 4),
                        Text(
                          c.message!,
                          style: TextStyle(
                              color: colors.textSecondary, fontSize: 12),
                        ),
                      ],
                      if (c.reason != null && c.reason!.isNotEmpty) ...[
                        const SizedBox(height: 2),
                        Text(
                          'Reason: ${c.reason!}',
                          style: TextStyle(
                              color: colors.textMuted, fontSize: 11),
                        ),
                      ],
                    ],
                  ),
                ),
              )),
        ],
      ],
    ];

    return CustomScrollView(
      slivers: [
        SliverPadding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
          sliver: SliverList.list(children: headerItems),
        ),
        if (apps.isEmpty)
          SliverPadding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            sliver: SliverList.list(
              children: [_EmptyCard(label: 'No generated applications.')],
            ),
          )
        else
          SliverPadding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            sliver: SliverList.builder(
              itemCount: apps.length,
              itemBuilder: (context, i) {
                final app = apps[i];
                return Card(
                  margin: const EdgeInsets.only(bottom: 6),
                  child: InkWell(
                    onTap: () {
                      final cid = ref.read(activeClusterProvider);
                      context.push(
                        '/clusters/$cid/gitops/applications/'
                        '${Uri.encodeComponent(app.id)}',
                      );
                    },
                    child: Padding(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 16, vertical: 10),
                      child: Row(
                        children: [
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  app.name,
                                  style: TextStyle(
                                    color: colors.accent,
                                    fontWeight: FontWeight.w600,
                                  ),
                                  overflow: TextOverflow.ellipsis,
                                ),
                                const SizedBox(height: 2),
                                Text(
                                  '${app.namespace}  ·  ${app.destinationNamespace ?? "—"}',
                                  style: TextStyle(
                                      color: colors.textMuted, fontSize: 12),
                                ),
                              ],
                            ),
                          ),
                          StatusPill(
                            label: app.syncStatus,
                            color: statusColor(colors, app.syncStatus),
                          ),
                        ],
                      ),
                    ),
                  ),
                );
              },
            ),
          ),
        SliverPadding(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
          sliver: SliverList.list(children: footerItems),
        ),
      ],
    );
  }
}

class _PanelHeader extends StatelessWidget {
  const _PanelHeader({required this.label, required this.colors});

  final String label;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Text(
        label,
        style: TextStyle(
          color: colors.textPrimary,
          fontWeight: FontWeight.w600,
          fontSize: 15,
        ),
      ),
    );
  }
}

class _EmptyCard extends StatelessWidget {
  const _EmptyCard({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Center(
          child: Text(label, style: TextStyle(color: colors.textMuted)),
        ),
      ),
    );
  }
}

/// Argo CD's generator block names. The first key from this list that
/// appears in `gen` is the type; otherwise the first key in the map
/// wins. Matches the web `GitOpsAppSetDetail.tsx` detection order.
const _generatorTypes = [
  'list',
  'git',
  'clusters',
  'matrix',
  'merge',
  'pullRequest',
  'scmProvider',
  'clusterDecisionResource',
  'plugin',
];

String _detectGeneratorType(Map<String, Object?> gen) {
  for (final t in _generatorTypes) {
    if (gen.containsKey(t)) return t;
  }
  if (gen.isEmpty) return 'unknown';
  return gen.keys.first;
}
