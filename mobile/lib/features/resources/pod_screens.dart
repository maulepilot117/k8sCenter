// Pod list + detail. Phone shows a card list, tablet a DataTable.
// Detail Overview surfaces phase, container count, restart count
// (including init + ephemeral containers, matching kubectl), node + IPs.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_list_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

/// Read-only thin view over the unstructured Pod map.
class _PodRow {
  _PodRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get phase =>
      readPath(raw, 'status.phase') as String? ?? 'Unknown';

  /// Sums restart counts across regular, init, and ephemeral container
  /// statuses — matches kubectl semantics. Pre-fix this only counted
  /// regular containers, hiding init-container CrashLoopBackOff (the
  /// most common oncall failure mode for stuck pods).
  int get restartCount {
    var total = 0;
    for (final field in const [
      'status.containerStatuses',
      'status.initContainerStatuses',
      'status.ephemeralContainerStatuses',
    ]) {
      final statuses = (readPath(raw, field) as List?) ?? const [];
      for (final s in statuses) {
        if (s is Map) {
          final r = s['restartCount'];
          if (r is num) total += r.toInt();
        }
      }
    }
    return total;
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

  /// Init containers that have completed (exitCode==0). Surfaced
  /// separately on the row so a Pod stuck on init shows "Init: 1/3"
  /// rather than misleadingly reporting all-ready via the regular
  /// container ratio.
  int get readyInitContainers {
    final statuses =
        (readPath(raw, 'status.initContainerStatuses') as List?) ?? const [];
    var done = 0;
    for (final s in statuses) {
      if (s is Map) {
        final state = s['state'];
        if (state is Map && state['terminated'] is Map) {
          final term = state['terminated'] as Map;
          if ((term['exitCode'] as num?)?.toInt() == 0) done++;
        }
      }
    }
    return done;
  }

  int get totalInitContainers {
    final inits = (readPath(raw, 'spec.initContainers') as List?) ?? const [];
    return inits.length;
  }

  String get podIP => readPath(raw, 'status.podIP') as String? ?? '—';
  String get nodeName => readPath(raw, 'spec.nodeName') as String? ?? '—';
}

class PodListScreen extends ConsumerWidget {
  const PodListScreen({super.key, this.namespace});

  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'Pods' : 'Pods · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'pods',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_PodRow.new).toList();
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
                value: (r) => r.totalInitContainers > 0 &&
                        r.readyInitContainers < r.totalInitContainers
                    ? 'Init ${r.readyInitContainers}/${r.totalInitContainers}'
                    : '${r.readyContainers}/${r.totalContainers}',
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
              kindDetailPath(
                clusterId: clusterId,
                kind: 'pods',
                namespace: r.meta.namespace,
                name: r.meta.name,
              ),
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
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: 'pods',
      namespace: namespace,
      name: name,
    );
    final get = ref.watch(resourceGetProvider(getKey));

    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(name)),
        body: ErrorStateView(
          message: e.toString(),
          onRetry: () => ref.invalidate(resourceGetProvider(getKey)),
        ),
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
                      value:
                          '${pod.readyContainers}/${pod.totalContainers} ready',
                    ),
                    if (pod.totalInitContainers > 0)
                      DetailRow(
                        label: 'Init containers',
                        value:
                            '${pod.readyInitContainers}/${pod.totalInitContainers} done',
                      ),
                    DetailRow(
                      label: 'Restarts',
                      value: '${pod.restartCount} '
                          '(sum across containers + init + ephemeral)',
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
