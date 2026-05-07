// Deployment list + detail.

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

class _DeploymentRow {
  _DeploymentRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  int get desired => (readPath(raw, 'spec.replicas') as num?)?.toInt() ?? 0;
  int get ready => (readPath(raw, 'status.readyReplicas') as num?)?.toInt() ?? 0;
  int get available =>
      (readPath(raw, 'status.availableReplicas') as num?)?.toInt() ?? 0;
  int get updated =>
      (readPath(raw, 'status.updatedReplicas') as num?)?.toInt() ?? 0;
  String get strategy =>
      readPath(raw, 'spec.strategy.type') as String? ?? 'RollingUpdate';
  bool get healthy => desired > 0 && ready == desired;
}

class DeploymentListScreen extends ConsumerWidget {
  const DeploymentListScreen({super.key, this.namespace});

  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(
            namespace == null ? 'Deployments' : 'Deployments · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'deployments',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_DeploymentRow.new).toList();
          return ResourceTable<_DeploymentRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(label: 'Namespace', value: (r) => r.meta.namespace),
              ResourceColumn(
                label: 'Ready',
                value: (r) => '${r.ready}/${r.desired}',
                color: (ctx, r) => r.healthy
                    ? Theme.of(ctx).extension<KubeColors>()!.success
                    : Theme.of(ctx).extension<KubeColors>()!.warning,
              ),
              ResourceColumn(label: 'Up-to-date', value: (r) => '${r.updated}'),
              ResourceColumn(label: 'Available', value: (r) => '${r.available}'),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              kindDetailPath(
                clusterId: clusterId,
                kind: 'deployments',
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

class DeploymentDetailScreen extends ConsumerWidget {
  const DeploymentDetailScreen({
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
      kind: 'deployments',
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
        final d = _DeploymentRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'Deployment',
          name: d.meta.name,
          namespace: d.meta.namespace,
          icon: Icons.dashboard_outlined,
          statusLabel: d.healthy ? 'Healthy' : 'Degraded',
          statusColor: d.healthy ? colors.success : colors.warning,
          resource: raw,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'REPLICAS',
                child: Column(
                  children: [
                    DetailRow(label: 'Desired', value: '${d.desired}'),
                    DetailRow(label: 'Ready', value: '${d.ready}'),
                    DetailRow(label: 'Up-to-date', value: '${d.updated}'),
                    DetailRow(label: 'Available', value: '${d.available}'),
                  ],
                ),
              ),
              DetailSection(
                title: 'STRATEGY',
                child: DetailRow(label: 'Type', value: d.strategy),
              ),
              if (d.meta.labels.isNotEmpty)
                DetailSection(
                  title: 'LABELS',
                  child: DetailRow(
                    label: 'Labels',
                    value: joinMap(d.meta.labels, maxEntries: 10),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}
