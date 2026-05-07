// PVC list + detail. Surfaces phase, capacity, access modes, and
// storage class — the four numbers oncall checks when a pod's stuck on
// FailedScheduling for "no available volume".

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

class _PvcRow {
  _PvcRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get phase => readPath(raw, 'status.phase') as String? ?? 'Unknown';
  String get volumeName =>
      readPath(raw, 'spec.volumeName') as String? ?? '—';
  String get storageClass =>
      readPath(raw, 'spec.storageClassName') as String? ?? '<default>';
  String get capacity =>
      '${readPath(raw, 'status.capacity.storage') ?? readPath(raw, 'spec.resources.requests.storage') ?? '—'}';
  String get accessModes {
    final modes = (readPath(raw, 'spec.accessModes') as List?) ?? const [];
    if (modes.isEmpty) return '—';
    return modes.whereType<String>().join(', ');
  }

  String get volumeMode =>
      readPath(raw, 'spec.volumeMode') as String? ?? 'Filesystem';
}

class PvcListScreen extends ConsumerWidget {
  const PvcListScreen({super.key, this.namespace});
  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'PVCs' : 'PVCs · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'pvcs',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_PvcRow.new).toList();
          return ResourceTable<_PvcRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(label: 'Namespace', value: (r) => r.meta.namespace),
              ResourceColumn(
                label: 'Status',
                value: (r) => r.phase,
                color: (ctx, r) {
                  final c = Theme.of(ctx).extension<KubeColors>()!;
                  return switch (r.phase) {
                    'Bound' => c.success,
                    'Pending' => c.warning,
                    'Lost' => c.error,
                    _ => c.textMuted,
                  };
                },
              ),
              ResourceColumn(label: 'Volume', value: (r) => r.volumeName),
              ResourceColumn(label: 'Capacity', value: (r) => r.capacity),
              ResourceColumn(
                label: 'Access modes',
                value: (r) => r.accessModes,
              ),
              ResourceColumn(
                label: 'Storage class',
                value: (r) => r.storageClass,
              ),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              kindDetailPath(
                clusterId: clusterId,
                kind: 'pvcs',
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

class PvcDetailScreen extends ConsumerWidget {
  const PvcDetailScreen({
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
      kind: 'pvcs',
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
        final p = _PvcRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        final statusColor = switch (p.phase) {
          'Bound' => colors.success,
          'Pending' => colors.warning,
          'Lost' => colors.error,
          _ => colors.textMuted,
        };
        return ResourceDetailScaffold(
          kindLabel: 'PersistentVolumeClaim',
          name: p.meta.name,
          namespace: p.meta.namespace,
          uid: p.meta.uid,
          icon: Icons.sd_storage_outlined,
          statusLabel: p.phase,
          statusColor: statusColor,
          resource: raw,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'STATUS',
                child: Column(
                  children: [
                    DetailRow(label: 'Phase', value: p.phase),
                    DetailRow(label: 'Volume', value: p.volumeName),
                  ],
                ),
              ),
              DetailSection(
                title: 'STORAGE',
                child: Column(
                  children: [
                    DetailRow(label: 'Capacity', value: p.capacity),
                    DetailRow(label: 'Access modes', value: p.accessModes),
                    DetailRow(label: 'Volume mode', value: p.volumeMode),
                    DetailRow(label: 'Storage class', value: p.storageClass),
                  ],
                ),
              ),
              if (p.meta.labels.isNotEmpty)
                DetailSection(
                  title: 'LABELS',
                  child: DetailRow(
                    label: 'Labels',
                    value: joinMap(p.meta.labels, maxEntries: 10),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}
