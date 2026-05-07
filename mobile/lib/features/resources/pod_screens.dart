// Pod list + detail. Phone shows a card list, tablet a DataTable.
// Detail Overview surfaces phase, container count, restart count,
// node + IPs.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

/// Read-only thin view over the unstructured Pod map.
class _PodRow {
  _PodRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get phase =>
      readPath(raw, 'status.phase') as String? ?? 'Unknown';

  int get restartCount {
    final statuses =
        (readPath(raw, 'status.containerStatuses') as List?) ?? const [];
    return statuses.fold<int>(0, (acc, s) {
      if (s is! Map) return acc;
      final r = s['restartCount'];
      return acc + (r is num ? r.toInt() : 0);
    });
  }

  int get readyContainers {
    final statuses =
        (readPath(raw, 'status.containerStatuses') as List?) ?? const [];
    var ready = 0;
    for (final s in statuses) {
      if (s is Map && s['ready'] == true) ready++;
    }
    return ready;
  }

  int get totalContainers {
    final containers =
        (readPath(raw, 'spec.containers') as List?) ?? const [];
    return containers.length;
  }

  String get podIP => readPath(raw, 'status.podIP') as String? ?? '—';
  String get nodeName => readPath(raw, 'spec.nodeName') as String? ?? '—';
}

class PodListScreen extends ConsumerWidget {
  const PodListScreen({super.key, this.namespace});

  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final list = ref.watch(
      resourceListProvider(
        ResourceListKey(kind: 'pods', namespace: namespace),
      ),
    );

    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'Pods' : 'Pods · $namespace'),
      ),
      body: list.when(
        loading: () => const LoadingState(),
        error: (e, _) => ErrorStateView(
          message: e.toString(),
          onRetry: () => ref.invalidate(
            resourceListProvider(
              ResourceListKey(kind: 'pods', namespace: namespace),
            ),
          ),
        ),
        data: (resp) {
          final rows = resp.items.map(_PodRow.new).toList();
          return ResourceTable<_PodRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(
                label: 'Namespace',
                value: (r) => r.meta.namespace,
              ),
              ResourceColumn(
                label: 'Status',
                value: (r) => r.phase,
                color: (ctx, r) {
                  final c = Theme.of(ctx).extension<KubeColors>()!;
                  switch (r.phase) {
                    case 'Running':
                      return c.success;
                    case 'Pending':
                      return c.warning;
                    case 'Failed':
                      return c.error;
                  }
                  return null;
                },
              ),
              ResourceColumn(
                label: 'Ready',
                value: (r) => '${r.readyContainers}/${r.totalContainers}',
              ),
              ResourceColumn(
                label: 'Restarts',
                value: (r) => '${r.restartCount}',
              ),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              '/clusters/local/workloads/pods/${r.meta.namespace}/${r.meta.name}',
            ),
          );
        },
      ),
    );
  }
}

class PodDetailScreen extends ConsumerWidget {
  const PodDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final get = ref.watch(
      resourceGetProvider(
        ResourceGetKey(kind: 'pods', namespace: namespace, name: name),
      ),
    );

    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(name)),
        body: ErrorStateView(message: e.toString()),
      ),
      data: (raw) {
        final pod = _PodRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        final statusColor = switch (pod.phase) {
          'Running' => colors.success,
          'Pending' => colors.warning,
          'Failed' => colors.error,
          _ => colors.textMuted,
        };
        return ResourceDetailScaffold(
          kindLabel: 'Pod',
          name: pod.meta.name,
          namespace: pod.meta.namespace,
          icon: Icons.bubble_chart_outlined,
          statusLabel: pod.phase,
          statusColor: statusColor,
          resource: raw,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'STATUS',
                child: Column(
                  children: [
                    DetailRow(label: 'Phase', value: pod.phase),
                    DetailRow(
                      label: 'Containers',
                      value: '${pod.readyContainers}/${pod.totalContainers} ready',
                    ),
                    DetailRow(
                      label: 'Restarts',
                      value: '${pod.restartCount}',
                    ),
                    DetailRow(label: 'Pod IP', value: pod.podIP),
                    DetailRow(label: 'Node', value: pod.nodeName),
                    DetailRow(
                      label: 'Created',
                      value: pod.meta.creationTimestamp.isEmpty
                          ? '—'
                          : '${pod.meta.creationTimestamp} (${formatAge(pod.meta.creationTimestamp)})',
                    ),
                  ],
                ),
              ),
              if (pod.meta.labels.isNotEmpty)
                DetailSection(
                  title: 'LABELS',
                  child: DetailRow(
                    label: 'Labels',
                    value: joinMap(pod.meta.labels, maxEntries: 10),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}
