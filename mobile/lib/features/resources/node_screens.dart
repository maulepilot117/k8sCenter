// Node list + detail. Cluster-scoped — no namespace segment.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

class _NodeRow {
  _NodeRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get readyStatus {
    final conditions =
        (readPath(raw, 'status.conditions') as List?) ?? const [];
    for (final c in conditions) {
      if (c is Map && c['type'] == 'Ready') {
        return c['status'] as String? ?? 'Unknown';
      }
    }
    return 'Unknown';
  }

  bool get ready => readyStatus == 'True';

  String get roles {
    final labels = meta.labels;
    final r = labels.keys
        .where((k) => k.startsWith('node-role.kubernetes.io/'))
        .map((k) => k.split('/').last)
        .toList();
    return r.isEmpty ? 'worker' : r.join(', ');
  }

  String get version =>
      readPath(raw, 'status.nodeInfo.kubeletVersion') as String? ?? '—';

  String get internalIP {
    final addrs = (readPath(raw, 'status.addresses') as List?) ?? const [];
    for (final a in addrs) {
      if (a is Map && a['type'] == 'InternalIP') {
        return a['address'] as String? ?? '—';
      }
    }
    return '—';
  }

  String get os => readPath(raw, 'status.nodeInfo.osImage') as String? ?? '—';

  String get architecture =>
      readPath(raw, 'status.nodeInfo.architecture') as String? ?? '—';

  String get containerRuntime =>
      readPath(raw, 'status.nodeInfo.containerRuntimeVersion') as String? ?? '—';

  String get podCapacity =>
      '${readPath(raw, 'status.capacity.pods') ?? '—'}';

  String get cpuCapacity =>
      '${readPath(raw, 'status.capacity.cpu') ?? '—'}';

  String get memCapacity =>
      '${readPath(raw, 'status.capacity.memory') ?? '—'}';
}

class NodeListScreen extends ConsumerWidget {
  const NodeListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final list = ref.watch(
      resourceListProvider(const ResourceListKey(kind: 'nodes')),
    );
    return Scaffold(
      appBar: AppBar(title: const Text('Nodes')),
      body: list.when(
        loading: () => const LoadingState(),
        error: (e, _) => ErrorStateView(message: e.toString()),
        data: (resp) {
          final rows = resp.items.map(_NodeRow.new).toList();
          return ResourceTable<_NodeRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(
                label: 'Status',
                value: (r) => r.ready ? 'Ready' : 'NotReady',
                color: (ctx, r) => r.ready
                    ? Theme.of(ctx).extension<KubeColors>()!.success
                    : Theme.of(ctx).extension<KubeColors>()!.error,
              ),
              ResourceColumn(label: 'Roles', value: (r) => r.roles),
              ResourceColumn(label: 'Version', value: (r) => r.version),
              ResourceColumn(label: 'Internal IP', value: (r) => r.internalIP),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              '/clusters/local/cluster/nodes/${r.meta.name}',
            ),
          );
        },
      ),
    );
  }
}

class NodeDetailScreen extends ConsumerWidget {
  const NodeDetailScreen({super.key, required this.name});

  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final get = ref.watch(
      resourceGetProvider(
        ResourceGetKey(kind: 'nodes', namespace: '', name: name),
      ),
    );
    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(name)),
        body: ErrorStateView(message: e.toString()),
      ),
      data: (raw) {
        final n = _NodeRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'Node',
          name: n.meta.name,
          icon: Icons.dns_outlined,
          statusLabel: n.ready ? 'Ready' : 'NotReady',
          statusColor: n.ready ? colors.success : colors.error,
          resource: raw,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'STATUS',
                child: Column(
                  children: [
                    DetailRow(label: 'Ready', value: n.readyStatus),
                    DetailRow(label: 'Roles', value: n.roles),
                    DetailRow(label: 'Version', value: n.version),
                  ],
                ),
              ),
              DetailSection(
                title: 'NODE INFO',
                child: Column(
                  children: [
                    DetailRow(label: 'OS', value: n.os),
                    DetailRow(label: 'Architecture', value: n.architecture),
                    DetailRow(label: 'Container Runtime', value: n.containerRuntime),
                    DetailRow(label: 'Internal IP', value: n.internalIP),
                  ],
                ),
              ),
              DetailSection(
                title: 'CAPACITY',
                child: Column(
                  children: [
                    DetailRow(label: 'CPU', value: n.cpuCapacity),
                    DetailRow(label: 'Memory', value: n.memCapacity),
                    DetailRow(label: 'Pods', value: n.podCapacity),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}
