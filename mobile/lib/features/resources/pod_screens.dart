// Pod list + detail. Phone shows a card list, tablet a DataTable.
// Detail Overview surfaces phase, container count, restart count
// (including init + ephemeral containers, matching kubectl), node + IPs.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/mesh_repository.dart';
import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../util/service_derivation.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_actions_button.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_list_scaffold.dart';
import '../../widgets/resource_table.dart';
// Intentional cross-feature import: Pod detail surfaces a Golden
// Signals tab from the mesh feature when a service mesh is detected
// and a Service targets this Pod's labels. PR-5f service-name
// autoderivation.
import '../mesh/golden_signals_tab.dart';
import '../observability/metrics/metrics_tab.dart';
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

  /// Names of regular + init + ephemeral containers, in declaration
  /// order. Used by the detail screen to render per-container "View
  /// logs" tiles. kubectl semantics: every container can have its own
  /// log stream, so the pod detail surfaces them all.
  List<String> get containerNames {
    final names = <String>[];
    for (final field in const [
      'spec.containers',
      'spec.initContainers',
      'spec.ephemeralContainers',
    ]) {
      final list = (readPath(raw, field) as List?) ?? const [];
      for (final c in list) {
        if (c is Map) {
          final n = c['name'] as String?;
          if (n != null && n.isNotEmpty) names.add(n);
        }
      }
    }
    return names;
  }
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

class PodDetailScreen extends ConsumerStatefulWidget {
  const PodDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  ConsumerState<PodDetailScreen> createState() => _PodDetailScreenState();
}

class _PodDetailScreenState extends ConsumerState<PodDetailScreen> {
  // Latch mesh status once detected so a transient autoDispose null
  // from `meshStatusProvider` doesn't shrink `extraTabs` while the
  // user is focused on the Golden Signals tab — the TabController
  // would otherwise see an out-of-bounds index. Mirrors the latching
  // pattern in ServiceDetailScreen.
  MeshStatus? _stableMeshStatus;

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: 'pods',
      namespace: widget.namespace,
      name: widget.name,
    );
    final get = ref.watch(resourceGetProvider(getKey));
    final currentMesh = ref.watch(meshStatusProvider(clusterId)).valueOrNull;
    if (currentMesh != null && currentMesh.isInstalled) {
      _stableMeshStatus = currentMesh;
    }
    final stableMesh = _stableMeshStatus;

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
        final pod = _PodRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        final statusColor = switch (pod.phase) {
          'Running' => colors.success,
          'Pending' => colors.warning,
          'Failed' => colors.error,
          _ => colors.textMuted,
        };
        // Derive candidate Services from this Pod's labels via the
        // memoized provider — same algorithm as before but cached on
        // (clusterId, namespace, labels) so detail-screen rebuilds
        // with identical inputs don't re-walk the Service list.
        final derivedServices = stableMesh != null
            ? ref.watch(derivedServicesProvider(DerivedServicesKey(
                clusterId: clusterId,
                namespace: pod.meta.namespace,
                resourceLabels: pod.meta.labels,
              )))
            : const <DerivedService>[];
        return ResourceDetailScaffold(
          kindLabel: 'Pod',
          name: pod.meta.name,
          namespace: pod.meta.namespace,
          uid: pod.meta.uid,
          icon: Icons.bubble_chart_outlined,
          statusLabel: pod.phase,
          statusColor: statusColor,
          resource: raw,
          trailingAction: ResourceActionsButton(
            kind: 'pods',
            namespace: pod.meta.namespace,
            name: pod.meta.name,
            resource: raw,
          ),
          extraTabs: [
            DetailExtraTab(
              label: 'Metrics',
              body: MetricsTab(
                clusterId: clusterId,
                kind: 'pods',
                namespace: pod.meta.namespace,
                name: pod.meta.name,
              ),
            ),
            if (derivedServices.isNotEmpty && stableMesh != null)
              DetailExtraTab(
                label: 'Golden signals',
                body: GoldenSignalsTab.fromCandidates(
                  namespace: pod.meta.namespace,
                  candidates:
                      derivedServices.map((s) => s.name).toList(),
                  status: stableMesh,
                ),
              ),
          ],
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
              if (pod.containerNames.isNotEmpty)
                DetailSection(
                  title: 'LOGS',
                  child: Column(
                    children: [
                      for (final c in pod.containerNames)
                        ListTile(
                          dense: true,
                          contentPadding: EdgeInsets.zero,
                          leading: ExcludeSemantics(
                            child: Icon(
                              Icons.terminal_outlined,
                              color: colors.accent,
                              size: 18,
                            ),
                          ),
                          title: Text(c,
                              style: TextStyle(
                                  color: colors.textPrimary, fontSize: 13)),
                          trailing: ExcludeSemantics(
                            child: Icon(Icons.chevron_right,
                                color: colors.textMuted, size: 18),
                          ),
                          onTap: () => context.push(
                            '/clusters/$clusterId/workloads/pods/'
                            '${Uri.encodeComponent(pod.meta.namespace)}/'
                            '${Uri.encodeComponent(pod.meta.name)}/logs/'
                            '${Uri.encodeComponent(c)}',
                          ),
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
