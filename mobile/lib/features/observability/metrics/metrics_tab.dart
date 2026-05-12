// Body widget for the per-resource Metrics tab. Composes the
// `TimeRangePicker` and a vertical scroll of `KubeLineChart` cards —
// one per panel defined in `metric_panels.dart` for the target kind.
//
// Status gating runs through `monitoringStatusProvider`. When the
// cluster doesn't have Prometheus (`detected: false`), the entire tab
// renders `FeatureUnavailableState.monitoring()`. Empty per-panel
// results render a "No data for this time range" banner rather than a
// flat-zero chart so operators can distinguish "metric missing" from
// "metric is zero".

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/monitoring_repository.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../../widgets/empty_states.dart';
import '../../../widgets/feature_unavailable_state.dart';
import '../../../widgets/kube_line_chart.dart';
import '../../../widgets/time_range_picker.dart';
import 'metric_panels.dart';
import 'metrics_controller.dart';

class MetricsTab extends ConsumerWidget {
  const MetricsTab({
    super.key,
    required this.clusterId,
    required this.kind,
    required this.namespace,
    required this.name,
  });

  final String clusterId;
  final String kind;
  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = ref.watch(monitoringStatusProvider(clusterId));
    return status.when(
      loading: () => const LoadingState(),
      error: (e, _) => ErrorStateView(
        message: 'Failed to query monitoring status: $e',
        onRetry: () => ref.invalidate(monitoringStatusProvider(clusterId)),
      ),
      data: (s) {
        if (!s.detected) {
          return FeatureUnavailableState.monitoring();
        }
        if (!metricsAvailableForKind(kind)) {
          return const _NoPanelsState();
        }
        final target = MetricsTarget(
          clusterId: clusterId,
          kind: kind,
          namespace: namespace,
          name: name,
        );
        return _MetricsBody(target: target);
      },
    );
  }
}

class _MetricsBody extends ConsumerWidget {
  const _MetricsBody({required this.target});

  final MetricsTarget target;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(metricsControllerProvider(target));
    final notifier = ref.read(metricsControllerProvider(target).notifier);

    return RefreshIndicator(
      onRefresh: notifier.refresh,
      child: ListView(
        padding: const EdgeInsets.symmetric(
          horizontal: 12,
          vertical: 12,
        ),
        children: [
          _TimeRangeBar(
            state: state,
            onChanged: (range) => notifier.setRange(
              range.start,
              range.end,
              _mapPreset(range.preset),
            ),
          ),
          const SizedBox(height: 12),
          ...metricPanelsByKind[target.kind]!.map((panel) {
            final status =
                state.panels[panel.id] ?? const PanelLoading();
            return _PanelCard(panel: panel, status: status);
          }),
        ],
      ),
    );
  }
}

class _TimeRangeBar extends StatelessWidget {
  const _TimeRangeBar({required this.state, required this.onChanged});

  final MetricsState state;
  final ValueChanged<TimeRange> onChanged;

  @override
  Widget build(BuildContext context) {
    final initial = (
      start: state.range.start,
      end: state.range.end,
      preset: _toWidgetPreset(state.range.preset),
    );
    return TimeRangePicker(initial: initial, onChanged: onChanged);
  }
}

class _PanelCard extends StatelessWidget {
  const _PanelCard({required this.panel, required this.status});

  final MetricPanel panel;
  final PanelStatus status;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  panel.title,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              if (panel.unitHint != null)
                Text(
                  panel.unitHint!,
                  style: TextStyle(color: colors.textMuted, fontSize: 11),
                ),
            ],
          ),
          const SizedBox(height: 8),
          SizedBox(height: 200, child: _PanelBody(panel: panel, status: status)),
        ],
      ),
    );
  }
}

class _PanelBody extends StatelessWidget {
  const _PanelBody({required this.panel, required this.status});

  final MetricPanel panel;
  final PanelStatus status;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return switch (status) {
      PanelLoading() => const LoadingState(),
      PanelFailed(:final message) => Center(
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            child: Text(
              message,
              textAlign: TextAlign.center,
              style: TextStyle(color: colors.error, fontSize: 12),
            ),
          ),
        ),
      PanelLoaded(:final result) => _renderResult(result),
    };
  }

  Widget _renderResult(QueryRangeResult result) {
    if (result.isEmpty) {
      return Builder(builder: (context) {
        final colors = Theme.of(context).extension<KubeColors>()!;
        return Center(
          child: Text(
            'No data for this time range',
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
        );
      });
    }
    final severity = _toChartSeverity(panel.severity);
    final series = result.series.map((s) {
      final label = _labelFor(s.labels);
      return (
        label: label,
        points: s.points,
        severity: severity,
      );
    }).toList();
    return KubeLineChart(
      series: series,
      showLegend: result.series.length > 1,
      // Title is rendered by the card chrome; chart-level title would
      // duplicate it.
    );
  }

  /// Picks the most operator-meaningful label key from a Prometheus
  /// series. Pods/containers expose `container`; network panels return
  /// no labels (single series) so fall back to the panel id.
  String _labelFor(Map<String, String> labels) {
    for (final key in const [
      'container',
      'pod',
      'instance',
      'node',
      'deployment',
      'statefulset',
      'daemonset',
    ]) {
      final v = labels[key];
      if (v != null && v.isNotEmpty) return v;
    }
    return panel.id;
  }
}

class _NoPanelsState extends StatelessWidget {
  const _NoPanelsState();

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.show_chart_outlined,
                size: 40, color: colors.textMuted),
            const SizedBox(height: 12),
            Text(
              'No curated metrics for this resource kind',
              textAlign: TextAlign.center,
              style: TextStyle(color: colors.textSecondary, fontSize: 14),
            ),
            const SizedBox(height: 4),
            Text(
              'Open Grafana on a desktop for ad-hoc PromQL.',
              textAlign: TextAlign.center,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Cross-layer enum bridges. The widget's TimePreset and the controller's
// MetricsPreset are intentionally separate to keep the controller free of
// widget imports; these helpers translate between the two.
// ---------------------------------------------------------------------------

MetricsPreset _mapPreset(TimePreset p) {
  switch (p) {
    case TimePreset.last15m:
      return MetricsPreset.last15m;
    case TimePreset.last1h:
      return MetricsPreset.last1h;
    case TimePreset.last6h:
      return MetricsPreset.last6h;
    case TimePreset.last24h:
      return MetricsPreset.last24h;
    case TimePreset.last7d:
      return MetricsPreset.last7d;
    case TimePreset.custom:
      return MetricsPreset.custom;
  }
}

TimePreset _toWidgetPreset(MetricsPreset p) {
  switch (p) {
    case MetricsPreset.last15m:
      return TimePreset.last15m;
    case MetricsPreset.last1h:
      return TimePreset.last1h;
    case MetricsPreset.last6h:
      return TimePreset.last6h;
    case MetricsPreset.last24h:
      return TimePreset.last24h;
    case MetricsPreset.last7d:
      return TimePreset.last7d;
    case MetricsPreset.custom:
      return TimePreset.custom;
  }
}

KubeChartSeverity _toChartSeverity(PanelSeverity s) {
  switch (s) {
    case PanelSeverity.primary:
      return KubeChartSeverity.primary;
    case PanelSeverity.success:
      return KubeChartSeverity.success;
    case PanelSeverity.warning:
      return KubeChartSeverity.warning;
    case PanelSeverity.error:
      return KubeChartSeverity.error;
    case PanelSeverity.info:
      return KubeChartSeverity.info;
    case PanelSeverity.muted:
      return KubeChartSeverity.muted;
  }
}
