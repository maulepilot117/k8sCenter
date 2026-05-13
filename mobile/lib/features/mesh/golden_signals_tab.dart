// Golden signals tile grid for a single Service. Surfaces under
// the Service detail screen as an `extraTabs` entry when mesh is
// detected on the cluster.
//
// **Tile grid, not LineCharts.** The backend exposes point-in-time
// scalars (rps / errorRate / p50 / p95 / p99), not time series — the
// plan's "4-up LineChart" wording predates the backend audit. Mobile
// renders five tiles in a responsive Wrap layout.
//
// Mesh disambiguation: when both Istio and Linkerd are installed the
// service's mesh is ambiguous (the backend can't auto-detect from the
// Service object alone). The tab presents a SegmentedButton so the
// operator picks; until they do, the body renders a prompt rather
// than 503-ing on every refresh.
//
// Missing queries: a non-empty `signals.missingQueries` is normal —
// the backend's PromQL timeout is 2s, and any failed sub-query lands
// here. The tab renders a banner naming the failed metrics; the
// associated tiles render an em-dash rather than `0.0`.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/mesh_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import 'mesh_widgets.dart';

/// Body of the Metrics-style tab on Service detail. The host
/// [Widget.build] is reached only when `meshStatus.isInstalled`
/// — the Service detail screen gates the tab visibility.
class GoldenSignalsTab extends ConsumerStatefulWidget {
  const GoldenSignalsTab({
    super.key,
    required this.namespace,
    required this.service,
    required this.status,
  });

  final String namespace;
  final String service;

  /// Resolved mesh status; carries `detected` so the tab knows
  /// whether to render the mesh picker (both installed) or auto-
  /// select the single installed mesh.
  final MeshStatus status;

  @override
  ConsumerState<GoldenSignalsTab> createState() => _GoldenSignalsTabState();
}

class _GoldenSignalsTabState extends ConsumerState<GoldenSignalsTab> {
  String? _mesh;

  @override
  void initState() {
    super.initState();
    _mesh = _deriveMesh(widget.status, null);
  }

  @override
  void didUpdateWidget(GoldenSignalsTab oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.status.detected == widget.status.detected) return;
    // Re-derive preferred mesh from the new status. If the currently
    // selected mesh is still installed, keep it; otherwise fall back to
    // the auto-derived choice (or null when both are still installed).
    final auto = _deriveMesh(widget.status, null);
    if (_mesh != null && !_isMeshInstalled(_mesh!, widget.status)) {
      // Previously selected mesh is no longer detected — clear.
      setState(() => _mesh = auto);
    } else if (auto != null && _mesh == null) {
      // Transitioned from both→one installed: auto-select.
      setState(() => _mesh = auto);
    }
  }

  /// Returns the auto-selected mesh when exactly one is installed, or
  /// null when both (or neither) are installed.
  static String? _deriveMesh(MeshStatus status, String? current) {
    if (status.hasIstio && !status.hasLinkerd) return 'istio';
    if (status.hasLinkerd && !status.hasIstio) return 'linkerd';
    return null;
  }

  static bool _isMeshInstalled(String mesh, MeshStatus status) {
    return mesh == 'istio' ? status.hasIstio : status.hasLinkerd;
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final clusterId = ref.watch(activeClusterProvider);
    // Reset mesh selection when the user switches clusters so the tab
    // re-derives from the new cluster's installed meshes instead of
    // carrying over the previous selection.
    ref.listen<String>(activeClusterProvider, (previous, next) {
      if (previous != next) {
        setState(() => _mesh = _deriveMesh(widget.status, null));
      }
    });

    return Column(
      children: [
        if (widget.status.hasBoth)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
            child: Row(
              children: [
                Text(
                  'Mesh',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: SegmentedButton<String>(
                    style: const ButtonStyle(visualDensity: VisualDensity.compact),
                    segments: const [
                      ButtonSegment(value: 'istio', label: Text('Istio')),
                      ButtonSegment(value: 'linkerd', label: Text('Linkerd')),
                    ],
                    selected: _mesh == null ? <String>{} : <String>{_mesh!},
                    emptySelectionAllowed: true,
                    onSelectionChanged: (s) => setState(
                      () => _mesh = s.isEmpty ? null : s.first,
                    ),
                  ),
                ),
              ],
            ),
          ),
        Expanded(
          child: _mesh == null && widget.status.hasBoth
              ? _MeshPickerPrompt()
              : _SignalsBody(
                  clusterId: clusterId,
                  namespace: widget.namespace,
                  service: widget.service,
                  mesh: _mesh,
                ),
        ),
      ],
    );
  }
}

class _MeshPickerPrompt extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          'Both meshes are installed. Pick one above to compute golden '
          'signals for this service — Istio and Linkerd publish '
          'different Prometheus series.',
          textAlign: TextAlign.center,
          style: TextStyle(color: colors.textMuted, fontSize: 13),
        ),
      ),
    );
  }
}

class _SignalsBody extends ConsumerWidget {
  const _SignalsBody({
    required this.clusterId,
    required this.namespace,
    required this.service,
    required this.mesh,
  });

  final String clusterId;
  final String namespace;
  final String service;
  final String? mesh;

  MeshGoldenSignalsKey get _key => MeshGoldenSignalsKey(
        clusterId: clusterId,
        namespace: namespace,
        service: service,
        mesh: mesh,
      );

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(meshGoldenSignalsProvider(_key));

    Future<void> handleRefresh() async {
      ref.invalidate(meshGoldenSignalsProvider(_key));
      try {
        await ref.read(meshGoldenSignalsProvider(_key).future);
      } on Object {
        // surfaces via error branch
      }
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _errorShell(e, handleRefresh, colors),
        data: (response) {
          final s = response.signals;
          return ListView(
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.fromLTRB(0, 8, 0, 24),
            children: [
              if (!s.available)
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 12),
                  child: MeshBanner(
                    color: colors.warning,
                    icon: Icons.warning_amber_outlined,
                    title: 'Metrics unavailable',
                    body: s.reason ?? 'Prometheus did not respond within 2s.',
                  ),
                ),
              if (s.available && s.missingQueries.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 12),
                  child: MeshBanner(
                    color: colors.warning,
                    icon: Icons.info_outline,
                    title: '${s.missingQueries.length} metric(s) unavailable',
                    body: s.missingQueries.join(', '),
                  ),
                ),
              _TileGrid(signals: s),
              const SizedBox(height: 12),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Text(
                  'Mesh: ${meshDisplayName(s.mesh)}  ·  '
                  'service=${s.service}  ·  namespace=${s.namespace}',
                  style: TextStyle(color: colors.textMuted, fontSize: 11),
                ),
              ),
            ],
          );
        },
      ),
    );
  }

  Widget _errorShell(Object e, Future<void> Function() retry, KubeColors c) {
    final body = e is ApiError && e.statusCode == 400
        ? 'Service or namespace missing. This tab is rendered with '
            'invalid arguments — please re-open this Service from the '
            'list.'
        : e is ApiError && e.statusCode == 403
            ? 'You lack permission to read mesh metrics for this '
                'namespace.'
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

class _TileGrid extends StatelessWidget {
  const _TileGrid({required this.signals});

  final GoldenSignals signals;

  String _format(double v, String unit) {
    if (v.isNaN || v.isInfinite) return '—';
    if (unit == '%') return '${(v * 100).toStringAsFixed(2)} %';
    if (unit == 'ms') return '${v.toStringAsFixed(1)} ms';
    return v.toStringAsFixed(2);
  }

  String _value(String key, double raw, String unit, {bool missing = false}) {
    if (missing || signals.isMetricMissing(key)) return '—';
    return _format(raw, unit);
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    // Backend's missing-query keys match the JSON tag basenames; we
    // pre-compute one source-of-truth bag here.
    final tiles = <_TileData>[
      _TileData(
        label: 'Requests / s',
        value: _value('rps', signals.rps, ''),
        color: colors.info,
        missing: signals.isMetricMissing('rps'),
      ),
      _TileData(
        label: 'Error rate',
        value: _value(
          'errorRate',
          signals.errorRate,
          '%',
          missing: signals.isMetricMissing('errorRate') ||
              signals.isMetricMissing('errorNum') ||
              signals.isMetricMissing('errorDen'),
        ),
        color: signals.isMetricMissing('errorRate') ||
                signals.isMetricMissing('errorNum') ||
                signals.isMetricMissing('errorDen')
            ? colors.textMuted
            : signals.errorRate > 0.05
                ? colors.error
                : colors.success,
        missing: signals.isMetricMissing('errorRate') ||
            signals.isMetricMissing('errorNum') ||
            signals.isMetricMissing('errorDen'),
      ),
      _TileData(
        label: 'p50 latency',
        value: _value('p50', signals.p50Ms, 'ms'),
        color: colors.accent,
        missing: signals.isMetricMissing('p50'),
      ),
      _TileData(
        label: 'p95 latency',
        value: _value('p95', signals.p95Ms, 'ms'),
        color: colors.accent,
        missing: signals.isMetricMissing('p95'),
      ),
      _TileData(
        label: 'p99 latency',
        value: _value('p99', signals.p99Ms, 'ms'),
        color: signals.isMetricMissing('p99')
            ? colors.textMuted
            : signals.p99Ms > 1000
                ? colors.warning
                : colors.accent,
        missing: signals.isMetricMissing('p99'),
      ),
    ];
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: LayoutBuilder(
        builder: (context, constraints) {
          final tileWidth = constraints.maxWidth < 540
              ? (constraints.maxWidth - 24) / 2
              : (constraints.maxWidth - 32) / 3;
          return Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final t in tiles)
                SizedBox(
                  width: tileWidth,
                  child: _Tile(data: t),
                ),
            ],
          );
        },
      ),
    );
  }
}

class _TileData {
  const _TileData({
    required this.label,
    required this.value,
    required this.color,
    required this.missing,
  });

  final String label;
  final String value;
  final Color color;
  final bool missing;
}

class _Tile extends StatelessWidget {
  const _Tile({required this.data});

  final _TileData data;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            data.label,
            style: TextStyle(color: colors.textMuted, fontSize: 11),
          ),
          const SizedBox(height: 6),
          Text(
            data.value,
            style: TextStyle(
              color: data.missing ? colors.textMuted : data.color,
              fontSize: 20,
              fontWeight: FontWeight.w700,
              fontFamily: 'monospace',
            ),
          ),
        ],
      ),
    );
  }
}
