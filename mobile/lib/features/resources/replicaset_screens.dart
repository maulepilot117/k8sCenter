// ReplicaSet list + detail. Surfaces owning Deployment via
// metadata.ownerReferences so operators can navigate up the
// Deployment → ReplicaSet → Pod chain.

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

class _ReplicaSetRow {
  _ReplicaSetRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  int get desired => (readPath(raw, 'spec.replicas') as num?)?.toInt() ?? 0;
  int get current =>
      (readPath(raw, 'status.replicas') as num?)?.toInt() ?? 0;
  int get ready => (readPath(raw, 'status.readyReplicas') as num?)?.toInt() ?? 0;

  /// Owner Deployment name (or '—' if no Deployment owner). RS that
  /// haven't been reaped after a rollback can have an owner-less state;
  /// the kubectl behavior is to render `<none>` and we mirror that.
  String get ownerDeployment {
    final owners =
        (readPath(raw, 'metadata.ownerReferences') as List?) ?? const [];
    for (final o in owners) {
      if (o is Map && o['kind'] == 'Deployment') {
        return o['name'] as String? ?? '—';
      }
    }
    return '<none>';
  }

  bool get healthy => desired > 0 ? ready == desired : current == 0;
}

class ReplicaSetListScreen extends ConsumerWidget {
  const ReplicaSetListScreen({super.key, this.namespace});
  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(
            namespace == null ? 'ReplicaSets' : 'ReplicaSets · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'replicasets',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_ReplicaSetRow.new).toList();
          return ResourceTable<_ReplicaSetRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(label: 'Namespace', value: (r) => r.meta.namespace),
              ResourceColumn(label: 'Desired', value: (r) => '${r.desired}'),
              ResourceColumn(label: 'Current', value: (r) => '${r.current}'),
              ResourceColumn(
                label: 'Ready',
                value: (r) => '${r.ready}',
                color: (ctx, r) => r.healthy
                    ? Theme.of(ctx).extension<KubeColors>()!.success
                    : Theme.of(ctx).extension<KubeColors>()!.warning,
              ),
              ResourceColumn(
                label: 'Owner',
                value: (r) => r.ownerDeployment,
              ),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              kindDetailPath(
                clusterId: clusterId,
                kind: 'replicasets',
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

class ReplicaSetDetailScreen extends ConsumerWidget {
  const ReplicaSetDetailScreen({
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
      kind: 'replicasets',
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
        final r = _ReplicaSetRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'ReplicaSet',
          name: r.meta.name,
          namespace: r.meta.namespace,
          uid: r.meta.uid,
          icon: Icons.layers_outlined,
          statusLabel: r.healthy ? 'Healthy' : 'Degraded',
          statusColor: r.healthy ? colors.success : colors.warning,
          resource: raw,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'REPLICAS',
                child: Column(
                  children: [
                    DetailRow(label: 'Desired', value: '${r.desired}'),
                    DetailRow(label: 'Current', value: '${r.current}'),
                    DetailRow(label: 'Ready', value: '${r.ready}'),
                  ],
                ),
              ),
              DetailSection(
                title: 'OWNER',
                child: DetailRow(
                  label: 'Deployment',
                  value: r.ownerDeployment,
                ),
              ),
              if (r.meta.labels.isNotEmpty)
                DetailSection(
                  title: 'LABELS',
                  child: DetailRow(
                    label: 'Labels',
                    value: joinMap(r.meta.labels, maxEntries: 10),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}
