// Namespace list + detail. Cluster-scoped — no namespace column.
// Phase chip renders red for Terminating so namespaces stuck on
// finalizers are obvious without drilling in.

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

class _NamespaceRow {
  _NamespaceRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get phase => readPath(raw, 'status.phase') as String? ?? 'Unknown';
  bool get terminating => phase == 'Terminating';

  /// Joined finalizer list (often `kubernetes` for default namespaces;
  /// CRDs add their own when their controllers expect cleanup). Surfaced
  /// because namespace-stuck-Terminating is almost always a finalizer
  /// that no controller is reconciling.
  String get finalizers {
    final f = (readPath(raw, 'spec.finalizers') as List?) ?? const [];
    return f.whereType<String>().join(', ');
  }
}

class NamespaceListScreen extends ConsumerWidget {
  const NamespaceListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Namespaces')),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(clusterId: clusterId, kind: 'namespaces'),
        builder: (context, result) {
          final rows = result.items.map(_NamespaceRow.new).toList();
          return ResourceTable<_NamespaceRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(
                label: 'Phase',
                value: (r) => r.phase,
                color: (ctx, r) {
                  final c = Theme.of(ctx).extension<KubeColors>()!;
                  return switch (r.phase) {
                    'Active' => c.success,
                    'Terminating' => c.error,
                    _ => c.textMuted,
                  };
                },
              ),
              ResourceColumn(
                label: 'Labels',
                value: (r) => joinMap(r.meta.labels, maxEntries: 3),
              ),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              kindDetailPath(
                clusterId: clusterId,
                kind: 'namespaces',
                namespace: '',
                name: r.meta.name,
              ),
            ),
          );
        },
      ),
    );
  }
}

class NamespaceDetailScreen extends ConsumerWidget {
  const NamespaceDetailScreen({super.key, required this.name});

  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: 'namespaces',
      namespace: '',
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
        final n = _NamespaceRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        final statusColor = switch (n.phase) {
          'Active' => colors.success,
          'Terminating' => colors.error,
          _ => colors.textMuted,
        };
        return ResourceDetailScaffold(
          kindLabel: 'Namespace',
          name: n.meta.name,
          uid: n.meta.uid,
          icon: Icons.folder_outlined,
          statusLabel: n.phase,
          statusColor: statusColor,
          resource: raw,
          trailingAction: ResourceActionsButton(
            kind: 'namespaces',
            namespace: '',
            name: n.meta.name,
            resource: raw,
          ),
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'STATUS',
                child: Column(
                  children: [
                    DetailRow(label: 'Phase', value: n.phase),
                    if (n.finalizers.isNotEmpty)
                      DetailRow(label: 'Finalizers', value: n.finalizers),
                  ],
                ),
              ),
              if (n.meta.labels.isNotEmpty)
                DetailSection(
                  title: 'LABELS',
                  child: DetailRow(
                    label: 'Labels',
                    value: joinMap(n.meta.labels, maxEntries: 20),
                  ),
                ),
              if (n.meta.annotations.isNotEmpty)
                DetailSection(
                  title: 'ANNOTATIONS',
                  child: DetailRow(
                    label: 'Annotations',
                    value: joinMap(n.meta.annotations, maxEntries: 10),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}
