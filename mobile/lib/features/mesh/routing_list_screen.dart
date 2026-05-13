// Routing list — every TrafficRoute the active user can read across
// both meshes. Filter chips for mesh (Both / Istio / Linkerd) and a
// free-text namespace filter at the top. Partial-fetch error map
// surfaces as a banner above the list. Mirrors the web's
// `MeshRoutingList.tsx` filter and banner strategy.
//
// Status gating: `meshStatusProvider` gates the surface — when neither
// mesh is detected the operator sees `FeatureUnavailableState.mesh()`
// rather than an empty list with no explanation.
//
// Tap row → `/clusters/<id>/mesh/routing/<encoded-id>`. The composite
// id round-trips through [MeshRouteId.tryParse] in the detail screen.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/mesh_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'mesh_widgets.dart';

enum _MeshFilter { all, istio, linkerd }

class MeshRoutingListScreen extends ConsumerStatefulWidget {
  const MeshRoutingListScreen({super.key, this.initialMesh});

  /// Pre-filter the mesh chip on mount. Drawer deep-links don't seed
  /// this; the dashboard cards do (Istio card → `?mesh=istio`).
  final String? initialMesh;

  @override
  ConsumerState<MeshRoutingListScreen> createState() =>
      _MeshRoutingListScreenState();
}

class _MeshRoutingListScreenState
    extends ConsumerState<MeshRoutingListScreen> {
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

    return Scaffold(
      appBar: AppBar(title: const Text('Mesh Routing')),
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
          return _RoutingBody(
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

class _RoutingBody extends ConsumerWidget {
  const _RoutingBody({
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

  MeshRoutingKey get _key => MeshRoutingKey(
        clusterId: clusterId,
        namespace: namespace.isEmpty ? null : namespace,
      );

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(meshRoutingProvider(_key));

    Future<void> handleRefresh() async {
      ref.invalidate(meshRoutingProvider(_key));
      try {
        await ref.read(meshRoutingProvider(_key).future);
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
          final filtered = _applyMeshFilter(response.routes);
          return ListView(
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: [
              _FilterStrip(
                mesh: mesh,
                namespaceCtl: namespaceCtl,
                onMeshChanged: onMeshChanged,
                onNamespaceChanged: onNamespaceChanged,
              ),
              MeshErrorsBanner(errors: response.errors),
              if (filtered.isEmpty)
                Padding(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 32,
                  ),
                  child: Center(
                    child: Text(
                      response.routes.isEmpty
                          ? 'No routing rules. Apply a VirtualService, '
                              'HTTPRoute, or similar to see entries here.'
                          : 'No routes match the current filters.',
                      style: TextStyle(color: colors.textMuted),
                      textAlign: TextAlign.center,
                    ),
                  ),
                )
              else ...[
                for (final route in filtered)
                  _RouteRow(
                    route: route,
                    onTap: () => context.push(
                      '/clusters/$clusterId/mesh/routing/'
                      '${Uri.encodeComponent(route.id)}',
                    ),
                  ),
              ],
            ],
          );
        },
      ),
    );
  }

  List<TrafficRoute> _applyMeshFilter(List<TrafficRoute> routes) {
    return routes.where((r) {
      if (mesh == _MeshFilter.istio && r.mesh != 'istio') return false;
      if (mesh == _MeshFilter.linkerd && r.mesh != 'linkerd') return false;
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
                    'Failed to load routing rules',
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
              prefixIcon: const Icon(Icons.folder_outlined, size: 18),
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

class _RouteRow extends StatelessWidget {
  const _RouteRow({required this.route, required this.onTap});

  final TrafficRoute route;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final destCount = route.destinations.length;
    final hostPreview = route.hosts.isEmpty
        ? null
        : route.hosts.length <= 2
            ? route.hosts.join(', ')
            : '${route.hosts.take(2).join(', ')} (+${route.hosts.length - 2})';
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    route.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontWeight: FontWeight.w600,
                      fontSize: 15,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                MeshPill(
                  label: meshDisplayName(route.mesh),
                  color: colors.accent,
                ),
              ],
            ),
            const SizedBox(height: 4),
            Row(
              children: [
                Text(
                  '${route.kind} · ${route.namespace}',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
                if (destCount > 0) ...[
                  const SizedBox(width: 8),
                  _MutedBadge(
                    label: '$destCount destination${destCount == 1 ? '' : 's'}',
                    colors: colors,
                  ),
                ],
              ],
            ),
            if (hostPreview != null) ...[
              const SizedBox(height: 4),
              Text(
                hostPreview,
                style: TextStyle(color: colors.textSecondary, fontSize: 11),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ],
            Divider(
              color: colors.borderSubtle,
              height: 18,
            ),
          ],
        ),
      ),
    );
  }
}

class _MutedBadge extends StatelessWidget {
  const _MutedBadge({required this.label, required this.colors});

  final String label;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(10),
        color: colors.bgElevated,
      ),
      child: Text(
        label,
        style: TextStyle(
          color: colors.textMuted,
          fontSize: 11,
        ),
      ),
    );
  }
}
