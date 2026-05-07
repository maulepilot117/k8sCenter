// Service list + detail.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

class _ServiceRow {
  _ServiceRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get type =>
      readPath(raw, 'spec.type') as String? ?? 'ClusterIP';
  String get clusterIP =>
      readPath(raw, 'spec.clusterIP') as String? ?? '—';
  String get externalIP {
    final ips = readPath(raw, 'spec.externalIPs') as List?;
    if (ips == null || ips.isEmpty) {
      // LoadBalancer ingress falls back here.
      final ingress = readPath(raw, 'status.loadBalancer.ingress') as List?;
      if (ingress != null && ingress.isNotEmpty) {
        final first = ingress.first;
        if (first is Map) {
          return (first['ip'] as String?) ?? (first['hostname'] as String?) ?? '—';
        }
      }
      return '—';
    }
    return ips.join(', ');
  }

  String get ports {
    final list = readPath(raw, 'spec.ports') as List?;
    if (list == null || list.isEmpty) return '—';
    return list.whereType<Map<dynamic, dynamic>>().map((p) {
      final port = p['port'];
      final protocol = p['protocol'] ?? 'TCP';
      return '$port/$protocol';
    }).join(', ');
  }
}

class ServiceListScreen extends ConsumerWidget {
  const ServiceListScreen({super.key, this.namespace});

  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final list = ref.watch(
      resourceListProvider(
        ResourceListKey(kind: 'services', namespace: namespace),
      ),
    );
    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'Services' : 'Services · $namespace'),
      ),
      body: list.when(
        loading: () => const LoadingState(),
        error: (e, _) => ErrorStateView(message: e.toString()),
        data: (resp) {
          final rows = resp.items.map(_ServiceRow.new).toList();
          return ResourceTable<_ServiceRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(label: 'Namespace', value: (r) => r.meta.namespace),
              ResourceColumn(label: 'Type', value: (r) => r.type),
              ResourceColumn(label: 'Cluster IP', value: (r) => r.clusterIP),
              ResourceColumn(label: 'External IP', value: (r) => r.externalIP),
              ResourceColumn(label: 'Ports', value: (r) => r.ports),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              '/clusters/local/networking/services/${r.meta.namespace}/${r.meta.name}',
            ),
          );
        },
      ),
    );
  }
}

class ServiceDetailScreen extends ConsumerWidget {
  const ServiceDetailScreen({
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
        ResourceGetKey(kind: 'services', namespace: namespace, name: name),
      ),
    );
    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(name)),
        body: ErrorStateView(message: e.toString()),
      ),
      data: (raw) {
        final s = _ServiceRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'Service',
          name: s.meta.name,
          namespace: s.meta.namespace,
          icon: Icons.lan_outlined,
          statusLabel: s.type,
          statusColor: colors.accent,
          resource: raw,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'NETWORK',
                child: Column(
                  children: [
                    DetailRow(label: 'Type', value: s.type),
                    DetailRow(label: 'Cluster IP', value: s.clusterIP),
                    DetailRow(label: 'External IP', value: s.externalIP),
                    DetailRow(label: 'Ports', value: s.ports),
                  ],
                ),
              ),
              if (s.meta.labels.isNotEmpty)
                DetailSection(
                  title: 'LABELS',
                  child: DetailRow(
                    label: 'Labels',
                    value: joinMap(s.meta.labels, maxEntries: 10),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}
