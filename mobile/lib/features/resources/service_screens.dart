// Service list + detail.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/mesh_repository.dart';
import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_actions_button.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_list_scaffold.dart';
import '../../widgets/resource_table.dart';
// Intentional cross-feature import: Service detail surfaces a golden
// signals tab from the mesh feature when a service mesh is detected on
// the cluster (added in PR-4f). This is the only mesh dependency in the
// resources feature.
import '../mesh/golden_signals_tab.dart';
import 'k8s_helpers.dart';

class _ServiceRow {
  _ServiceRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get type => readPath(raw, 'spec.type') as String? ?? 'ClusterIP';
  String get clusterIP => readPath(raw, 'spec.clusterIP') as String? ?? '—';

  /// External IP/hostname. Joins all entries from `spec.externalIPs` and
  /// `status.loadBalancer.ingress` (post-fix: previously dropped all but
  /// the first ingress entry, hiding multi-IP LoadBalancer setups).
  String get externalIP {
    final out = <String>[];
    final external = (readPath(raw, 'spec.externalIPs') as List?) ?? const [];
    out.addAll(external.whereType<String>());
    final ingress =
        (readPath(raw, 'status.loadBalancer.ingress') as List?) ?? const [];
    for (final entry in ingress) {
      if (entry is Map) {
        final v = (entry['ip'] as String?) ?? (entry['hostname'] as String?);
        if (v != null && v.isNotEmpty) out.add(v);
      }
    }
    return out.isEmpty ? '—' : out.join(', ');
  }

  /// Renders ports as `name:port:nodePort/protocol` when name and
  /// nodePort are present (multi-port LoadBalancer/NodePort). Falls
  /// back to `port/protocol` for the single-port ClusterIP common case.
  String get ports {
    final list = readPath(raw, 'spec.ports') as List?;
    if (list == null || list.isEmpty) return '—';
    return list
        .whereType<Map<dynamic, dynamic>>()
        .map((p) {
          final name = p['name'] as String?;
          final port = p['port'];
          final nodePort = p['nodePort'];
          final protocol = (p['protocol'] as String?) ?? 'TCP';
          final base = nodePort == null ? '$port' : '$port:$nodePort';
          final body = '$base/$protocol';
          return name == null || name.isEmpty ? body : '$name:$body';
        })
        .join(', ');
  }
}

class ServiceListScreen extends ConsumerWidget {
  const ServiceListScreen({super.key, this.namespace});

  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'Services' : 'Services · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'services',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_ServiceRow.new).toList();
          return ResourceTable<_ServiceRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(
                label: 'Namespace',
                value: (r) => r.meta.namespace,
              ),
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
              kindDetailPath(
                clusterId: clusterId,
                kind: 'services',
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

class ServiceDetailScreen extends ConsumerStatefulWidget {
  const ServiceDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  ConsumerState<ServiceDetailScreen> createState() =>
      _ServiceDetailScreenState();
}

class _ServiceDetailScreenState extends ConsumerState<ServiceDetailScreen> {
  // Latches the first observed `isInstalled` mesh status and keeps the
  // Golden Signals tab visible for the rest of this screen's lifetime,
  // even if subsequent mesh status polls transiently 5xx and report
  // `MeshStatus.empty`. See issue #260: without this, an active tab
  // selection vanishes mid-incident on a passing backend hiccup.
  //
  // Trade-off: a deliberate mesh uninstall while the screen is open
  // still shows the tab until the user navigates away and back. That
  // surface is rare and self-correcting; tab-flicker on transient 5xx
  // is the common case and harms operator UX during incidents.
  MeshStatus? _stableMeshStatus;

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: 'services',
      namespace: widget.namespace,
      name: widget.name,
    );
    final get = ref.watch(resourceGetProvider(getKey));

    // Latch the first non-empty mesh status, then keep subsequent
    // non-empty updates for fresh data in the tab body. Empty/null
    // emits are ignored once we've latched, preserving tab visibility.
    final currentMesh = ref.watch(meshStatusProvider(clusterId)).valueOrNull;
    if (currentMesh != null && currentMesh.isInstalled) {
      _stableMeshStatus = currentMesh;
    }

    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(widget.name)),
        body: ErrorStateView(
          message: e.toString(),
          onRetry: () => ref.invalidate(resourceGetProvider(getKey)),
        ),
      ),
      data: (raw) {
        final s = _ServiceRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        // M4 PR-4f: surface golden signals as an extra tab when a mesh
        // is installed on the cluster. The mesh status drives both tab
        // visibility and (in the tab itself) the mesh disambiguation
        // when both Istio and Linkerd are present.
        final stableMesh = _stableMeshStatus;
        final extraTabs = <DetailExtraTab>[
          if (stableMesh != null)
            DetailExtraTab(
              label: 'Golden signals',
              body: GoldenSignalsTab(
                namespace: s.meta.namespace,
                service: s.meta.name,
                status: stableMesh,
              ),
            ),
        ];
        return ResourceDetailScaffold(
          kindLabel: 'Service',
          name: s.meta.name,
          namespace: s.meta.namespace,
          uid: s.meta.uid,
          icon: Icons.lan_outlined,
          statusLabel: s.type,
          statusColor: colors.accent,
          resource: raw,
          extraTabs: extraTabs,
          trailingAction: ResourceActionsButton(
            kind: 'services',
            namespace: s.meta.namespace,
            name: s.meta.name,
            resource: raw,
          ),
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
