// Themed multi-series line chart used by the per-resource Metrics tab
// (PR-4b) and golden signals surface (PR-4e). All colors route through
// KubeColors so the chart adapts to the operator's active theme without
// any hardcoded hex literals.
//
// X-axis label strategy mirrors the web frontend: ranges ≤ 24 h show
// HH:mm; longer ranges show MM/dd HH:mm. This keeps the axis readable
// on a 360dp phone screen without truncation.

import 'package:fl_chart/fl_chart.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../theme/kube_theme_builder.dart';

/// Severity → KubeColors mapping that drives line colors. Kept in one
/// enum so the caller declares intent ("this series is an error metric")
/// and the chart resolves the token, avoiding per-callsite color
/// decision fatigue.
enum KubeChartSeverity { primary, success, warning, error, info, muted }

/// A single timestamped data point.
typedef MetricsPoint = ({DateTime t, double v});

/// One named line in the chart, associated with a display color via
/// [severity].
typedef MetricsSeries = ({
  String label,
  List<MetricsPoint> points,
  KubeChartSeverity severity,
});

/// Multi-series line chart backed by `fl_chart`. Intended for
/// CPU/memory/network/latency time-series from
/// `GET /v1/monitoring/query_range`.
class KubeLineChart extends StatelessWidget {
  const KubeLineChart({
    required this.series,
    this.title,
    this.showGrid = true,
    this.showLegend = true,
    this.height = 200,
    super.key,
  });

  final List<MetricsSeries> series;
  final String? title;
  final bool showGrid;
  final bool showLegend;
  final double height;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final hasData = series.any((s) => s.points.isNotEmpty);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (title != null) _ChartTitle(title: title!, colors: colors),
        SizedBox(
          height: height,
          child: hasData
              ? _LineChartBody(
                  series: series,
                  colors: colors,
                  showGrid: showGrid,
                )
              : _NoDataPlaceholder(colors: colors),
        ),
        if (showLegend && hasData)
          _Legend(series: series, colors: colors),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Private helpers
// ---------------------------------------------------------------------------

class _ChartTitle extends StatelessWidget {
  const _ChartTitle({required this.title, required this.colors});

  final String title;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Text(
        title,
        style: TextStyle(
          color: colors.textSecondary,
          fontSize: 13,
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}

class _NoDataPlaceholder extends StatelessWidget {
  const _NoDataPlaceholder({required this.colors});

  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Text(
        'No data for this time range',
        style: TextStyle(color: colors.textMuted, fontSize: 13),
      ),
    );
  }
}

class _Legend extends StatelessWidget {
  const _Legend({required this.series, required this.colors});

  final List<MetricsSeries> series;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Wrap(
        spacing: 16,
        runSpacing: 4,
        children: series.map((s) {
          final color = _severityColor(s.severity, colors);
          return Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 12,
                height: 3,
                decoration: BoxDecoration(
                  color: color,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              const SizedBox(width: 4),
              Text(
                s.label,
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ],
          );
        }).toList(),
      ),
    );
  }
}

class _LineChartBody extends StatelessWidget {
  const _LineChartBody({
    required this.series,
    required this.colors,
    required this.showGrid,
  });

  final List<MetricsSeries> series;
  final KubeColors colors;
  final bool showGrid;

  @override
  Widget build(BuildContext context) {
    final allPoints = series.expand((s) => s.points).toList();
    final minTs = allPoints.map((p) => p.t).reduce(
      (a, b) => a.isBefore(b) ? a : b,
    );
    final maxTs = allPoints.map((p) => p.t).reduce(
      (a, b) => a.isAfter(b) ? a : b,
    );
    final rangeHours = maxTs.difference(minTs).inHours;
    // Short ranges: show HH:mm; longer ranges: include date.
    final labelFmt = rangeHours <= 24
        ? DateFormat('HH:mm')
        : DateFormat('MM/dd HH:mm');

    final barData = series.map((s) {
      final color = _severityColor(s.severity, colors);
      final spots = s.points
          .map((p) => FlSpot(
                p.t.millisecondsSinceEpoch.toDouble(),
                p.v,
              ))
          .toList();
      return LineChartBarData(
        spots: spots,
        color: color,
        barWidth: 1.5,
        dotData: const FlDotData(show: false),
        belowBarData: BarAreaData(
          show: true,
          color: color.withValues(alpha: 0.08),
        ),
      );
    }).toList();

    return LineChart(
      LineChartData(
        lineBarsData: barData,
        gridData: FlGridData(
          show: showGrid,
          drawVerticalLine: false,
          getDrawingHorizontalLine: (_) => FlLine(
            color: colors.borderSubtle,
            strokeWidth: 0.5,
          ),
        ),
        borderData: FlBorderData(show: false),
        titlesData: FlTitlesData(
          topTitles:
              const AxisTitles(sideTitles: SideTitles(showTitles: false)),
          rightTitles:
              const AxisTitles(sideTitles: SideTitles(showTitles: false)),
          bottomTitles: AxisTitles(
            sideTitles: SideTitles(
              showTitles: true,
              reservedSize: 28,
              interval: _xInterval(minTs, maxTs),
              getTitlesWidget: (value, meta) {
                final dt =
                    DateTime.fromMillisecondsSinceEpoch(value.toInt());
                return SideTitleWidget(
                  axisSide: meta.axisSide,
                  child: Text(
                    labelFmt.format(dt),
                    style: TextStyle(
                      color: colors.textMuted,
                      fontSize: 10,
                    ),
                  ),
                );
              },
            ),
          ),
          leftTitles: AxisTitles(
            sideTitles: SideTitles(
              showTitles: true,
              reservedSize: 40,
              getTitlesWidget: (value, meta) => SideTitleWidget(
                axisSide: meta.axisSide,
                child: Text(
                  _formatY(value),
                  style: TextStyle(
                    color: colors.textMuted,
                    fontSize: 10,
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  // Target ~4 x-axis labels regardless of range.
  double _xInterval(DateTime min, DateTime max) {
    final rangeMs = max.millisecondsSinceEpoch - min.millisecondsSinceEpoch;
    return (rangeMs / 4).clamp(1, double.infinity);
  }

  String _formatY(double v) {
    if (v >= 1e9) return '${(v / 1e9).toStringAsFixed(1)}G';
    if (v >= 1e6) return '${(v / 1e6).toStringAsFixed(1)}M';
    if (v >= 1e3) return '${(v / 1e3).toStringAsFixed(1)}k';
    return v.toStringAsFixed(v.truncate() == v ? 0 : 1);
  }
}

/// Maps a [KubeChartSeverity] to the corresponding [KubeColors] token.
/// Kept as a top-level function so both the line chart and bar chart
/// can share the same mapping without duplication.
Color _severityColor(KubeChartSeverity severity, KubeColors colors) {
  switch (severity) {
    case KubeChartSeverity.primary:
      return colors.accent;
    case KubeChartSeverity.success:
      return colors.success;
    case KubeChartSeverity.warning:
      return colors.warning;
    case KubeChartSeverity.error:
      return colors.error;
    case KubeChartSeverity.info:
      return colors.info;
    case KubeChartSeverity.muted:
      return colors.textMuted;
  }
}
