// DaemonSet list + detail. The five status numbers (desired, current,
// ready, up-to-date, available) come straight from the controller and
// answer the only question oncall asks: "is this rolling out cleanly
// across every matched node, or is one stuck?".

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

class _DaemonSetRow {
  _DaemonSetRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  int get desired =>
      (readPath(raw, 'status.desiredNumberScheduled') as num?)?.toInt() ?? 0;
  int get current =>
      (readPath(raw, 'status.currentNumberScheduled') as num?)?.toInt() ?? 0;
  int get ready =>
      (readPath(raw, 'status.numberReady') as num?)?.toInt() ?? 0;
  int get upToDate =>
      (readPath(raw, 'status.updatedNumberScheduled') as num?)?.toInt() ?? 0;
  int get available =>
      (readPath(raw, 'status.numberAvailable') as num?)?.toInt() ?? 0;
  int get misscheduled =>
      (readPath(raw, 'status.numberMisscheduled') as num?)?.toInt() ?? 0;

  String get nodeSelector {
    final ns =
        readPath(raw, 'spec.template.spec.nodeSelector') as Map?  ?? const {};
    if (ns.isEmpty) return '<all nodes>';
    return ns.entries.map((e) => '${e.key}=${e.value}').join(', ');
  }

  bool get healthy =>
      desired > 0 && ready == desired && upToDate == desired && misscheduled == 0;
}

class DaemonSetListScreen extends ConsumerWidget {
  const DaemonSetListScreen({super.key, this.namespace});
  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(
            namespace == null ? 'DaemonSets' : 'DaemonSets · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'daemonsets',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_DaemonSetRow.new).toList();
          return ResourceTable<_DaemonSetRow>(
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
                label: 'Up-to-date',
                value: (r) => '${r.upToDate}',
              ),
              ResourceColumn(label: 'Available', value: (r) => '${r.available}'),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              kindDetailPath(
                clusterId: clusterId,
                kind: 'daemonsets',
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

class DaemonSetDetailScreen extends ConsumerWidget {
  const DaemonSetDetailScreen({
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
      kind: 'daemonsets',
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
        final d = _DaemonSetRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'DaemonSet',
          name: d.meta.name,
          namespace: d.meta.namespace,
          uid: d.meta.uid,
          icon: Icons.workspaces_outline,
          statusLabel: d.healthy ? 'Healthy' : 'Degraded',
          statusColor: d.healthy ? colors.success : colors.warning,
          resource: raw,
          trailingAction: ResourceActionsButton(
            kind: 'daemonsets',
            namespace: d.meta.namespace,
            name: d.meta.name,
            resource: raw,
          ),
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'ROLLOUT',
                child: Column(
                  children: [
                    DetailRow(label: 'Desired', value: '${d.desired}'),
                    DetailRow(label: 'Current', value: '${d.current}'),
                    DetailRow(label: 'Ready', value: '${d.ready}'),
                    DetailRow(label: 'Up-to-date', value: '${d.upToDate}'),
                    DetailRow(label: 'Available', value: '${d.available}'),
                    if (d.misscheduled > 0)
                      DetailRow(
                        label: 'Misscheduled',
                        value: '${d.misscheduled}',
                      ),
                  ],
                ),
              ),
              DetailSection(
                title: 'PLACEMENT',
                child: DetailRow(
                  label: 'Node selector',
                  value: d.nodeSelector,
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}
