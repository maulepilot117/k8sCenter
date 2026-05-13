// Routing CRD detail. Composite-ID-driven — the URL path segment is
// `Uri.encodeComponent(route.id)` where the id has the shape
// `mesh:namespace:kindCode:name`. The id round-trips through
// [MeshRouteId.tryParse] so an invalid path produces a 404-style
// error screen instead of a stack trace.
//
// Body layout:
//   Header card    — name + Mesh pill + Kind label
//   Metadata       — name / namespace / mesh / kind
//   Routing panel  — hosts / gateways / subsets (collapses empty lists)
//   Destinations   — numbered list with host + subset + port + weight
//   Matchers       — indexed list with method + path
//   Raw spec       — JSON-encoded raw map for operators who want the
//                    full unstructured payload (mobile doesn't ship the
//                    full YAML editor here; the desktop covers that
//                    case with a real CodeMirror-class surface).
//
// Effect annotations (Istio AuthorizationPolicy `effect: deny_all`)
// are part of `policies`, not `routing`, so they don't render here.

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/mesh_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../util/composite_id.dart';
import 'mesh_widgets.dart';

class MeshRouteDetailScreen extends ConsumerWidget {
  const MeshRouteDetailScreen({super.key, required this.id});

  /// Raw composite id as it arrived in the route path (already
  /// decoded one layer by go_router). Form: `mesh:ns:kindCode:name`.
  final String id;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final parsed = MeshRouteId.tryParse(id);

    if (parsed == null) {
      return Scaffold(
        appBar: AppBar(title: const Text('Route')),
        body: Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(
              'Invalid route ID: $id',
              style: TextStyle(color: colors.error),
              textAlign: TextAlign.center,
            ),
          ),
        ),
      );
    }

    final clusterId = ref.watch(activeClusterProvider);
    final key = MeshRouteDetailKey(clusterId: clusterId, id: id);
    final async = ref.watch(meshRouteDetailProvider(key));

    return Scaffold(
      appBar: AppBar(
        title: Text(parsed.name),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: () => ref.invalidate(meshRouteDetailProvider(key)),
          ),
        ],
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) {
          if (e is ApiError && e.statusCode == 404) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  'Route ${parsed.name} not found in namespace '
                  '${parsed.namespace}. It may have been deleted.',
                  textAlign: TextAlign.center,
                ),
              ),
            );
          }
          return Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Text(
                e is ApiError ? e.message : e.toString(),
                style: TextStyle(color: colors.error),
                textAlign: TextAlign.center,
              ),
            ),
          );
        },
        data: (route) => _Body(route: route, parsed: parsed),
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.route, required this.parsed});

  final TrafficRoute route;
  final MeshRouteId parsed;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ListView(
      padding: const EdgeInsets.fromLTRB(0, 8, 0, 24),
      children: [
        _Header(route: route, parsed: parsed),
        MeshSection(
          title: 'METADATA',
          child: Column(
            children: [
              MeshKvLine(label: 'Name', value: route.name),
              MeshKvLine(label: 'Namespace', value: route.namespace),
              MeshKvLine(
                label: 'Mesh',
                value: meshDisplayName(route.mesh),
                valueColor: colors.accent,
              ),
              MeshKvLine(label: 'Kind', value: route.kind),
              if (route.selector != null)
                MeshKvLine(
                  label: 'Selector',
                  value: route.selector!,
                  monospace: true,
                ),
            ],
          ),
        ),
        if (route.hosts.isNotEmpty ||
            route.gateways.isNotEmpty ||
            route.subsets.isNotEmpty)
          MeshSection(
            title: 'ROUTING',
            child: Column(
              children: [
                if (route.hosts.isNotEmpty)
                  MeshKvLine(
                    label: 'Hosts',
                    value: route.hosts.join('\n'),
                    monospace: true,
                  ),
                if (route.gateways.isNotEmpty)
                  MeshKvLine(
                    label: 'Gateways',
                    value: route.gateways.join('\n'),
                    monospace: true,
                  ),
                if (route.subsets.isNotEmpty)
                  MeshKvLine(
                    label: 'Subsets',
                    value: route.subsets.join(', '),
                  ),
              ],
            ),
          ),
        if (route.destinations.isNotEmpty)
          MeshSection(
            title: 'DESTINATIONS (${route.destinations.length})',
            child: Column(
              children: [
                for (var i = 0; i < route.destinations.length; i++)
                  _DestinationRow(index: i + 1, dest: route.destinations[i]),
              ],
            ),
          ),
        if (route.matchers.isNotEmpty)
          MeshSection(
            title: 'MATCHERS (${route.matchers.length})',
            child: Column(
              children: [
                for (var i = 0; i < route.matchers.length; i++)
                  _MatcherRow(index: i + 1, matcher: route.matchers[i]),
              ],
            ),
          ),
        if (route.raw != null) _RawPanel(raw: route.raw!),
      ],
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.route, required this.parsed});

  final TrafficRoute route;
  final MeshRouteId parsed;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Row(
        children: [
          Icon(Icons.alt_route, color: colors.accent),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  route.name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontWeight: FontWeight.w700,
                    fontSize: 18,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 2),
                Text(
                  '${route.kind} · ${route.namespace}',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
              ],
            ),
          ),
          MeshPill(
            label: meshDisplayName(route.mesh),
            color: colors.accent,
          ),
        ],
      ),
    );
  }
}

class _DestinationRow extends StatelessWidget {
  const _DestinationRow({required this.index, required this.dest});

  final int index;
  final RouteDestination dest;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final segments = <String>[
      if (dest.host != null) dest.host!,
      if (dest.subset != null) 'subset=${dest.subset}',
      if (dest.port != null) ':${dest.port}',
      if (dest.weight != null) 'weight=${dest.weight}',
    ];
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 24,
            child: Text(
              '$index.',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
          Expanded(
            child: SelectableText(
              segments.join(' '),
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 13,
                fontFamily: 'monospace',
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _MatcherRow extends StatelessWidget {
  const _MatcherRow({required this.index, required this.matcher});

  final int index;
  final RouteMatcher matcher;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final path = matcher.pathExact ??
        (matcher.pathPrefix != null ? '${matcher.pathPrefix}* (prefix)' : null) ??
        (matcher.pathRegex != null ? '${matcher.pathRegex} (regex)' : null) ??
        '—';
    final method = matcher.method ?? 'ANY';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 24,
            child: Text(
              '$index.',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
          MeshPill(label: method, color: colors.info),
          const SizedBox(width: 6),
          Expanded(
            child: SelectableText(
              path,
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 13,
                fontFamily: 'monospace',
              ),
            ),
          ),
          if (matcher.name != null)
            Padding(
              padding: const EdgeInsets.only(left: 8),
              child: Text(
                matcher.name!,
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ),
        ],
      ),
    );
  }
}

class _RawPanel extends StatefulWidget {
  const _RawPanel({required this.raw});

  final Map<String, Object?> raw;

  @override
  State<_RawPanel> createState() => _RawPanelState();
}

class _RawPanelState extends State<_RawPanel> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final encoded =
        const JsonEncoder.withIndent('  ').convert(widget.raw);
    return MeshSection(
      title: 'RAW SPEC',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InkWell(
            onTap: () => setState(() => _expanded = !_expanded),
            child: Row(
              children: [
                Icon(
                  _expanded ? Icons.expand_less : Icons.expand_more,
                  size: 18,
                  color: colors.textMuted,
                ),
                const SizedBox(width: 4),
                Text(
                  _expanded ? 'Hide' : 'Show',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
              ],
            ),
          ),
          if (_expanded) ...[
            const SizedBox(height: 6),
            Container(
              decoration: BoxDecoration(
                color: colors.bgSurface,
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: colors.borderSubtle),
              ),
              padding: const EdgeInsets.all(8),
              child: SelectableText(
                encoded,
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 11,
                  fontFamily: 'monospace',
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
