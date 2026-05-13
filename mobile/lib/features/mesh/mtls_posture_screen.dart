// mTLS posture per namespace. Namespace is required — backend hard-
// requires `?namespace=` (400 without it) so the screen presents a
// namespace prompt first, and only fetches once one is selected.
//
// Three-source attribution per row (Policy / Metric / Default) lives
// in the `WorkloadMTLS.source` field. The detail expansion shows
// `istioMode` and `sourceDetail` (workload / namespace / mesh scope)
// for Istio rows; Linkerd rows show only state + source.
//
// Workloads with `workloadKindConfident == false` carry an asterisk +
// tooltip explaining the kind was inferred from a ReplicaSet name
// heuristic — not a real owner-reference walk.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/mesh_repository.dart';
import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'mesh_widgets.dart';

class MeshMtlsPostureScreen extends ConsumerStatefulWidget {
  const MeshMtlsPostureScreen({super.key, this.initialNamespace});

  /// Drawer / deep-link seed value for the namespace.
  final String? initialNamespace;

  @override
  ConsumerState<MeshMtlsPostureScreen> createState() =>
      _MeshMtlsPostureScreenState();
}

class _MeshMtlsPostureScreenState
    extends ConsumerState<MeshMtlsPostureScreen> {
  String? _namespace;

  @override
  void initState() {
    super.initState();
    _namespace = widget.initialNamespace;
  }

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    // Reset namespace selection when the user switches clusters so stale
    // namespace state from the previous cluster doesn't contaminate the
    // new cluster's mTLS fetch.
    ref.listen<String>(activeClusterProvider, (previous, next) {
      if (previous != next) setState(() => _namespace = null);
    });
    final statusAsync = ref.watch(meshStatusProvider(clusterId));

    return Scaffold(
      appBar: AppBar(
        title: const Text('mTLS posture'),
      ),
      body: statusAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(e.toString()),
          ),
        ),
        data: (status) {
          if (!status.isInstalled) return FeatureUnavailableState.mesh();
          return Column(
            children: [
              _NamespaceBar(
                clusterId: clusterId,
                value: _namespace,
                onChanged: (v) => setState(() => _namespace = v),
              ),
              const Divider(height: 1),
              Expanded(
                child: _namespace == null || _namespace!.isEmpty
                    ? const _NamespacePrompt()
                    : _PostureBody(
                        clusterId: clusterId,
                        namespace: _namespace!,
                      ),
              ),
            ],
          );
        },
      ),
    );
  }
}

class _NamespaceBar extends ConsumerWidget {
  const _NamespaceBar({
    required this.clusterId,
    required this.value,
    required this.onChanged,
  });

  final String clusterId;
  final String? value;
  final ValueChanged<String?> onChanged;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final listAsync = ref.watch(resourceListProvider(
      ResourceListKey(clusterId: clusterId, kind: 'namespaces'),
    ));
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
      child: Row(
        children: [
          Icon(Icons.folder_outlined, color: colors.textMuted, size: 18),
          const SizedBox(width: 8),
          Expanded(
            child: listAsync.when(
              loading: () => Text(
                'Loading namespaces…',
                style: TextStyle(color: colors.textMuted, fontSize: 13),
              ),
              error: (e, _) => Row(
                children: [
                  Expanded(
                    child: TextField(
                      decoration: const InputDecoration(
                        isDense: true,
                        hintText: 'Namespace unavailable — type manually',
                        hintStyle: TextStyle(fontSize: 13),
                      ),
                      onSubmitted: onChanged,
                    ),
                  ),
                  const SizedBox(width: 8),
                  IconButton(
                    icon: const Icon(Icons.refresh, size: 20),
                    tooltip: 'Retry namespace list',
                    onPressed: () => ref.invalidate(
                      resourceListProvider(
                        ResourceListKey(
                          clusterId: clusterId,
                          kind: 'namespaces',
                        ),
                      ),
                    ),
                  ),
                ],
              ),
              data: (list) {
                final names = <String>{};
                for (final item in list.items) {
                  final meta = item['metadata'];
                  if (meta is Map) {
                    final n = meta['name'];
                    if (n is String && n.isNotEmpty) names.add(n);
                  }
                }
                final sorted = names.toList()..sort();
                return DropdownButton<String?>(
                  value: sorted.contains(value) ? value : null,
                  isExpanded: true,
                  hint: const Text('Select namespace'),
                  onChanged: onChanged,
                  items: [
                    for (final name in sorted)
                      DropdownMenuItem(value: name, child: Text(name)),
                  ],
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _NamespacePrompt extends StatelessWidget {
  const _NamespacePrompt();

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.lock_outline, size: 40, color: colors.textMuted),
            const SizedBox(height: 12),
            Text(
              'Choose a namespace',
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 16,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 6),
            Text(
              'mTLS posture is computed per namespace. Pick one above to '
              'see workload-level encryption state, source attribution, '
              'and Istio mode.',
              textAlign: TextAlign.center,
              style: TextStyle(color: colors.textMuted, fontSize: 13),
            ),
          ],
        ),
      ),
    );
  }
}

class _PostureBody extends ConsumerWidget {
  const _PostureBody({required this.clusterId, required this.namespace});

  final String clusterId;
  final String namespace;

  MeshMtlsKey get _key =>
      MeshMtlsKey(clusterId: clusterId, namespace: namespace);

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(meshMtlsPostureProvider(_key));

    Future<void> handleRefresh() async {
      ref.invalidate(meshMtlsPostureProvider(_key));
      try {
        await ref.read(meshMtlsPostureProvider(_key).future);
      } on Object {
        // surfaces via .when error branch
      }
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _errorShell(e, handleRefresh, colors),
        data: (response) {
          final workloads = response.workloads;
          final summary = _summarise(workloads);
          return ListView(
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: [
              MeshErrorsBanner(errors: response.errors),
              _SummaryStrip(summary: summary),
              if (workloads.isEmpty)
                Padding(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 32,
                  ),
                  child: Center(
                    child: Text(
                      'No workloads found in namespace `$namespace`. '
                      'The namespace may be empty or you may lack '
                      'pod-read permission.',
                      style: TextStyle(color: colors.textMuted),
                      textAlign: TextAlign.center,
                    ),
                  ),
                )
              else ...[
                for (final w in workloads) _WorkloadRow(workload: w),
              ],
            ],
          );
        },
      ),
    );
  }

  _PostureSummary _summarise(List<WorkloadMTLS> rows) {
    int active = 0, inactive = 0, mixed = 0, unmeshed = 0, unknown = 0;
    for (final w in rows) {
      switch (w.state.toLowerCase()) {
        case 'active':
          active++;
          break;
        case 'inactive':
          inactive++;
          break;
        case 'mixed':
          mixed++;
          break;
        case 'unmeshed':
          unmeshed++;
          break;
        default:
          unknown++;
          break;
      }
    }
    return _PostureSummary(
      active: active,
      inactive: inactive,
      mixed: mixed,
      unmeshed: unmeshed,
      unknown: unknown,
    );
  }

  Widget _errorShell(Object e, Future<void> Function() retry, KubeColors c) {
    final body = e is ApiError && e.statusCode == 400
        ? 'mTLS posture requires a namespace. Re-select above.'
        : e is ApiError && e.statusCode == 403
            ? 'You lack permission to view mTLS posture in this namespace.'
            : e is ApiError
                ? e.message
                : e.toString();
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [
        SizedBox(
          height: 280,
          child: Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    body,
                    style: TextStyle(color: c.textPrimary),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 12),
                  OutlinedButton(
                    onPressed: retry,
                    child: const Text('Retry'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class _PostureSummary {
  const _PostureSummary({
    required this.active,
    required this.inactive,
    required this.mixed,
    required this.unmeshed,
    this.unknown = 0,
  });

  final int active;
  final int inactive;
  final int mixed;
  final int unmeshed;

  /// Count of workloads with a state value not in the known vocabulary.
  /// Shown when non-zero so unknown backend states surface visibly.
  final int unknown;
}

class _SummaryStrip extends StatelessWidget {
  const _SummaryStrip({required this.summary});

  final _PostureSummary summary;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          MeshPill(label: '${summary.active} Active', color: colors.success),
          MeshPill(label: '${summary.mixed} Mixed', color: colors.warning),
          MeshPill(label: '${summary.inactive} Inactive', color: colors.error),
          MeshPill(
              label: '${summary.unmeshed} Unmeshed', color: colors.textMuted),
          if (summary.unknown > 0)
            MeshPill(
                label: '${summary.unknown} Unknown', color: colors.textMuted),
        ],
      ),
    );
  }
}

class _WorkloadRow extends StatelessWidget {
  const _WorkloadRow({required this.workload});

  final WorkloadMTLS workload;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final kind = workload.workloadKind;
    final showAsterisk = kind != null && !workload.workloadKindConfident;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      workload.workload,
                      style: TextStyle(
                        color: colors.textPrimary,
                        fontWeight: FontWeight.w600,
                        fontSize: 14,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                    if (kind != null)
                      Row(
                        children: [
                          Text(
                            kind,
                            style:
                                TextStyle(color: colors.textMuted, fontSize: 12),
                          ),
                          if (showAsterisk)
                            Tooltip(
                              message:
                                  'Workload kind inferred from ReplicaSet '
                                  'name (no owner-reference lookup).',
                              child: Padding(
                                padding: const EdgeInsets.only(left: 2),
                                child: Text(
                                  '*',
                                  style: TextStyle(
                                    color: colors.warning,
                                    fontSize: 12,
                                    fontWeight: FontWeight.w700,
                                  ),
                                ),
                              ),
                            ),
                        ],
                      ),
                  ],
                ),
              ),
              MeshPill(
                label: workload.state,
                color: meshStateColor(colors, workload.state),
              ),
            ],
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 4,
            children: [
              MeshPill(
                label: meshDisplayName(workload.mesh),
                color: colors.accent,
              ),
              _SourceBadge(source: workload.source),
              if (workload.istioMode != null)
                MeshPill(
                  label: 'Mode: ${workload.istioMode}',
                  color: meshStateColor(colors, workload.istioMode),
                ),
              if (workload.sourceDetail != null)
                MeshMutedBadge(
                  label: 'Scope: ${workload.sourceDetail}',
                ),
            ],
          ),
          Divider(color: colors.borderSubtle, height: 18),
        ],
      ),
    );
  }
}

class _SourceBadge extends StatelessWidget {
  const _SourceBadge({required this.source});

  final String source;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final color = switch (source) {
      'policy' => colors.info,
      'metric' => colors.accent,
      'default' => colors.textMuted,
      _ => colors.textMuted,
    };
    return MeshPill(label: 'Source: $source', color: color);
  }
}

