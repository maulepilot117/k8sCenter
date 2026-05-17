// Policies list — every MeshedPolicy (PeerAuthentication,
// AuthorizationPolicy, MeshTLSAuthentication, Server, etc.) the active
// user can read across both meshes. Filter chips for mesh
// (Both / Istio / Linkerd) and a free-text namespace filter at the top.
// Partial-fetch error map surfaces as a banner above the list. Mirrors
// `routing_list_screen.dart` filter and banner strategy.
//
// Status gating: `meshStatusProvider` gates the surface — when neither
// mesh is detected the operator sees `FeatureUnavailableState.mesh()`
// rather than an empty list with no explanation.
//
// No detail tap target today — the per-policy detail screen is deferred.
// Each row surfaces enough information (kind, mtls mode / action / effect,
// rule count, selector preview) for operator triage without it.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/mesh_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'mesh_widgets.dart';

enum _MeshFilter { all, istio, linkerd }

class MeshPoliciesListScreen extends ConsumerStatefulWidget {
  const MeshPoliciesListScreen({super.key, this.initialMesh});

  /// Pre-filter the mesh chip on mount. Dashboard CTAs may seed this
  /// (Istio card → `?mesh=istio`).
  final String? initialMesh;

  @override
  ConsumerState<MeshPoliciesListScreen> createState() =>
      _MeshPoliciesListScreenState();
}

class _MeshPoliciesListScreenState
    extends ConsumerState<MeshPoliciesListScreen> {
  late _MeshFilter _mesh;
  String _namespace = '';
  final _namespaceCtl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _mesh = switch (widget.initialMesh) {
      'istio' => _MeshFilter.istio,
      'linkerd' => _MeshFilter.linkerd,
      _ => _MeshFilter.all,
    };
  }

  @override
  void dispose() {
    _namespaceCtl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(meshStatusProvider(clusterId));
    // Reset mesh filter and namespace text when the user switches
    // clusters so stale filter state from the previous cluster is cleared.
    ref.listen<String>(activeClusterProvider, (previous, next) {
      if (previous != next) {
        setState(() {
          _mesh = _MeshFilter.all;
          _namespace = '';
          _namespaceCtl.clear();
        });
      }
    });

    return Scaffold(
      appBar: AppBar(title: const Text('Mesh Policies')),
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
          return _PoliciesBody(
            clusterId: clusterId,
            mesh: _mesh,
            namespace: _namespace,
            namespaceCtl: _namespaceCtl,
            onMeshChanged: (v) => setState(() => _mesh = v),
            onNamespaceChanged: (v) => setState(() => _namespace = v),
          );
        },
      ),
    );
  }
}

class _PoliciesBody extends ConsumerWidget {
  const _PoliciesBody({
    required this.clusterId,
    required this.mesh,
    required this.namespace,
    required this.namespaceCtl,
    required this.onMeshChanged,
    required this.onNamespaceChanged,
  });

  final String clusterId;
  final _MeshFilter mesh;
  final String namespace;
  final TextEditingController namespaceCtl;
  final ValueChanged<_MeshFilter> onMeshChanged;
  final ValueChanged<String> onNamespaceChanged;

  MeshPoliciesKey get _key => MeshPoliciesKey(
    clusterId: clusterId,
    namespace: namespace.isEmpty ? null : namespace,
  );

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(meshPoliciesProvider(_key));

    Future<void> handleRefresh() async {
      ref.invalidate(meshPoliciesProvider(_key));
      try {
        await ref.read(meshPoliciesProvider(_key).future);
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
          final filtered = _applyMeshFilter(response.policies);
          return CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: Column(
                  children: [
                    _FilterStrip(
                      mesh: mesh,
                      namespaceCtl: namespaceCtl,
                      onMeshChanged: onMeshChanged,
                      onNamespaceChanged: onNamespaceChanged,
                    ),
                    MeshErrorsBanner(errors: response.errors),
                  ],
                ),
              ),
              if (filtered.isEmpty)
                SliverToBoxAdapter(
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 16,
                      vertical: 32,
                    ),
                    child: Center(
                      child: Text(
                        response.policies.isEmpty
                            ? 'No mesh policies. Apply a '
                                  'PeerAuthentication, AuthorizationPolicy, '
                                  'or similar to see entries here.'
                            : 'No policies match the current filters.',
                        style: TextStyle(color: colors.textMuted),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                )
              else
                SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) => _PolicyRow(policy: filtered[index]),
                    childCount: filtered.length,
                  ),
                ),
              const SliverPadding(padding: EdgeInsets.only(bottom: 8)),
            ],
          );
        },
      ),
    );
  }

  List<MeshedPolicy> _applyMeshFilter(List<MeshedPolicy> policies) {
    return policies.where((p) {
      if (mesh == _MeshFilter.istio && p.mesh != 'istio') return false;
      if (mesh == _MeshFilter.linkerd && p.mesh != 'linkerd') return false;
      return true;
    }).toList();
  }

  Widget _errorShell(Object e, Future<void> Function() retry, KubeColors c) {
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
                    'Failed to load mesh policies',
                    style: TextStyle(
                      color: c.textPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    e is ApiError ? e.message : e.toString(),
                    style: TextStyle(color: c.textMuted),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 12),
                  OutlinedButton(onPressed: retry, child: const Text('Retry')),
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class _FilterStrip extends StatelessWidget {
  const _FilterStrip({
    required this.mesh,
    required this.namespaceCtl,
    required this.onMeshChanged,
    required this.onNamespaceChanged,
  });

  final _MeshFilter mesh;
  final TextEditingController namespaceCtl;
  final ValueChanged<_MeshFilter> onMeshChanged;
  final ValueChanged<String> onNamespaceChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(
            spacing: 6,
            runSpacing: 4,
            children: [
              for (final value in _MeshFilter.values)
                ChoiceChip(
                  label: Text(switch (value) {
                    _MeshFilter.all => 'Both meshes',
                    _MeshFilter.istio => 'Istio',
                    _MeshFilter.linkerd => 'Linkerd',
                  }),
                  selected: value == mesh,
                  onSelected: (_) => onMeshChanged(value),
                ),
            ],
          ),
          const SizedBox(height: 8),
          TextField(
            controller: namespaceCtl,
            decoration: InputDecoration(
              prefixIcon: ExcludeSemantics(
                child: const Icon(Icons.folder_outlined, size: 18),
              ),
              labelText: 'Namespace filter',
              hintText: 'Empty = all namespaces',
              hintStyle: TextStyle(color: colors.textMuted, fontSize: 12),
              isDense: true,
            ),
            onSubmitted: onNamespaceChanged,
            onChanged: (v) {
              // Debounce-light: only refetch on Enter via onSubmitted.
              // Free-text edits update local state but don't refire the
              // provider; otherwise every keystroke would trigger a fetch.
              if (v.isEmpty) onNamespaceChanged('');
            },
          ),
        ],
      ),
    );
  }
}

class _PolicyRow extends StatelessWidget {
  const _PolicyRow({required this.policy});

  final MeshedPolicy policy;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final statusLabel = _statusLabel(policy);
    final statusColor = meshStateColor(colors, statusLabel);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  policy.name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontWeight: FontWeight.w600,
                    fontSize: 15,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              MeshPill(
                label: meshDisplayName(policy.mesh),
                color: colors.accent,
              ),
            ],
          ),
          const SizedBox(height: 4),
          Row(
            children: [
              Expanded(
                child: Text(
                  policy.namespace.isEmpty
                      ? policy.kind
                      : '${policy.kind} · ${policy.namespace}',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              if (statusLabel != null) ...[
                const SizedBox(width: 8),
                MeshPill(label: statusLabel, color: statusColor),
              ],
              if (policy.ruleCount > 0) ...[
                const SizedBox(width: 6),
                MeshMutedBadge(
                  label:
                      '${policy.ruleCount} rule${policy.ruleCount == 1 ? '' : 's'}',
                ),
              ],
            ],
          ),
          if (policy.selector != null && policy.selector!.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text(
              policy.selector!,
              style: TextStyle(color: colors.textSecondary, fontSize: 11),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
          Divider(color: colors.borderSubtle, height: 18),
        ],
      ),
    );
  }

  // PeerAuthentication policies carry an mTLS mode; AuthorizationPolicy
  // carries an action (and optionally a backend-computed effect — prefer
  // the more specific action). Everything else has no top-level status
  // worth a pill.
  String? _statusLabel(MeshedPolicy p) {
    if (p.mtlsMode != null && p.mtlsMode!.isNotEmpty) return p.mtlsMode;
    if (p.action != null && p.action!.isNotEmpty) return p.action;
    if (p.effect != null && p.effect!.isNotEmpty) return p.effect;
    return null;
  }
}
