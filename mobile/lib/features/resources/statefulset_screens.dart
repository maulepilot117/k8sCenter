// StatefulSet list + detail. Surfaces the headless service name and
// volumeClaimTemplate count alongside the ready/desired ratio — the
// three numbers oncall actually checks before deciding to roll back.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_actions_button.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_list_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

class _StatefulSetRow {
  _StatefulSetRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  int get desired => (readPath(raw, 'spec.replicas') as num?)?.toInt() ?? 0;
  int get ready => (readPath(raw, 'status.readyReplicas') as num?)?.toInt() ?? 0;
  int get current =>
      (readPath(raw, 'status.currentReplicas') as num?)?.toInt() ?? 0;
  int get updated =>
      (readPath(raw, 'status.updatedReplicas') as num?)?.toInt() ?? 0;
  String get serviceName =>
      readPath(raw, 'spec.serviceName') as String? ?? '—';
  int get volumeClaimTemplateCount {
    final t = (readPath(raw, 'spec.volumeClaimTemplates') as List?) ?? const [];
    return t.length;
  }

  String get updateStrategy =>
      readPath(raw, 'spec.updateStrategy.type') as String? ?? 'RollingUpdate';

  bool get healthy => desired > 0 && ready == desired;
}

class StatefulSetListScreen extends ConsumerWidget {
  const StatefulSetListScreen({super.key, this.namespace});
  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(
            namespace == null ? 'StatefulSets' : 'StatefulSets · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'statefulsets',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_StatefulSetRow.new).toList();
          return ResourceTable<_StatefulSetRow>(
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
              ResourceColumn(label: 'Service', value: (r) => r.serviceName),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              kindDetailPath(
                clusterId: clusterId,
                kind: 'statefulsets',
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

class StatefulSetDetailScreen extends ConsumerWidget {
  const StatefulSetDetailScreen({
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
      kind: 'statefulsets',
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
        final s = _StatefulSetRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'StatefulSet',
          name: s.meta.name,
          namespace: s.meta.namespace,
          uid: s.meta.uid,
          icon: Icons.storage_outlined,
          statusLabel: s.healthy ? 'Healthy' : 'Degraded',
          statusColor: s.healthy ? colors.success : colors.warning,
          resource: raw,
          trailingAction: ResourceActionsButton(
            kind: 'statefulsets',
            namespace: s.meta.namespace,
            name: s.meta.name,
            resource: raw,
          ),
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'REPLICAS',
                child: Column(
                  children: [
                    DetailRow(label: 'Desired', value: '${s.desired}'),
                    DetailRow(label: 'Ready', value: '${s.ready}'),
                    DetailRow(label: 'Current', value: '${s.current}'),
                    DetailRow(label: 'Updated', value: '${s.updated}'),
                  ],
                ),
              ),
              DetailSection(
                title: 'SERVICE',
                child: DetailRow(label: 'Headless', value: s.serviceName),
              ),
              DetailSection(
                title: 'STORAGE',
                child: DetailRow(
                  label: 'Volume claim templates',
                  value: '${s.volumeClaimTemplateCount}',
                ),
              ),
              DetailSection(
                title: 'UPDATE STRATEGY',
                child: DetailRow(label: 'Type', value: s.updateStrategy),
              ),
            ],
          ),
        );
      },
    );
  }
}
