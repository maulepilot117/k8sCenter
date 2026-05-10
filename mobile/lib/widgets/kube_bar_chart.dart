// Single-series bar chart used for log volume histograms (PR-4c) and
// ESO sync rate panels (PR-4g). Reuses KubeChartSeverity and the
// shared kubeChartSeverityColor resolver from kube_line_chart.dart so
// the color contract is identical across both chart types.

import 'package:fl_chart/fl_chart.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../theme/kube_theme_builder.dart';
import 'kube_line_chart.dart' show KubeChartSeverity, kubeChartSeverityColor;

/// A single timestamped bar value.
typedef BarPoint = ({DateTime t, double v});

/// One bar series. Single-series only — the bar chart surface is
/// for "volume over time" reads, not multi-metric comparison.
typedef BarSeries = ({
  String label,
  List<BarPoint> points,
  KubeChartSeverity severity,
});

/// Single-series bar chart backed by `fl_chart`. Intended for the
/// LogQL volume histogram and the ESO sync-rate panel.
class KubeBarChart extends StatelessWidget {
  const KubeBarChart({
    required this.series,
    this.title,
    this.height = 160,
    super.key,
  });

  final BarSeries series;
  final String? title;
  final double height;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (title != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(
              title!,
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
        SizedBox(
          height: height,
          child: series.points.isEmpty
              ? Center(
                  child: Text(
                    'No volume data',
                    style: TextStyle(color: colors.textMuted, fontSize: 13),
                  ),
                )
              : _BarChartBody(series: series, colors: colors),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Private helper
// ---------------------------------------------------------------------------

class _BarChartBody extends StatelessWidget {
  const _BarChartBody({required this.series, required this.colors});

  final BarSeries series;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    // Drop non-finite points (NaN/Infinity from Prometheus rate windows
    // or stale samples), sort by timestamp, and merge duplicates so the
    // index-keyed bottom-axis label lookup picks the right bucket.
    final points = _normalize(series.points);
    if (points.isEmpty) {
      return Center(
        child: Text(
          'No volume data',
          style: TextStyle(color: colors.textMuted, fontSize: 13),
        ),
      );
    }
    final minTs = points.map((p) => p.t).reduce((a, b) => a.isBefore(b) ? a : b);
    final maxTs = points.map((p) => p.t).reduce((a, b) => a.isAfter(b) ? a : b);
    final rangeHours = maxTs.difference(minTs).inHours;
    final labelFmt = rangeHours <= 24
        ? DateFormat('HH:mm')
        : DateFormat('MM/dd HH:mm');

    final barColor = kubeChartSeverityColor(series.severity, colors);

    final groups = points.asMap().entries.map((e) {
      return BarChartGroupData(
        x: e.key,
        barRods: [
          BarChartRodData(
            toY: e.value.v,
            color: barColor,
            width: _barWidth(points.length),
            borderRadius: const BorderRadius.vertical(
              top: Radius.circular(2),
            ),
          ),
        ],
      );
    }).toList();

    // Show ~4 evenly-spaced x labels.
    final step = (points.length / 4).ceil().clamp(1, points.length);

    return BarChart(
      BarChartData(
        barGroups: groups,
        gridData: FlGridData(
          show: true,
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
              getTitlesWidget: (value, meta) {
                final idx = value.toInt();
                if (idx % step != 0 || idx >= points.length) {
                  return const SizedBox.shrink();
                }
                return SideTitleWidget(
                  axisSide: meta.axisSide,
                  child: Text(
                    labelFmt.format(points[idx].t),
                    style: TextStyle(color: colors.textMuted, fontSize: 10),
                  ),
                );
              },
            ),
          ),
          leftTitles: AxisTitles(
            sideTitles: SideTitles(
              showTitles: true,
              reservedSize: 36,
              getTitlesWidget: (value, meta) => SideTitleWidget(
                axisSide: meta.axisSide,
                child: Text(
                  _formatY(value),
                  style: TextStyle(color: colors.textMuted, fontSize: 10),
                ),
              ),
            ),
          ),
        ),
        barTouchData: BarTouchData(enabled: false),
      ),
    );
  }

  /// Filters non-finite values, sorts by timestamp, and sums values
  /// at duplicate timestamps so the histogram shows one bar per
  /// bucket. Backend merge paths (federated Prometheus, Loki shard
  /// interleave) can emit duplicates that fl_chart would otherwise
  /// stack as ambiguous adjacent bars at the same time.
  List<BarPoint> _normalize(List<BarPoint> raw) {
    final clean = raw.where((p) => p.v.isFinite).toList()
      ..sort((a, b) => a.t.compareTo(b.t));
    if (clean.length <= 1) return clean;
    final merged = <BarPoint>[];
    for (final p in clean) {
      if (merged.isNotEmpty && merged.last.t == p.t) {
        final prev = merged.removeLast();
        merged.add((t: prev.t, v: prev.v + p.v));
      } else {
        merged.add(p);
      }
    }
    return merged;
  }

  double _barWidth(int count) {
    if (count <= 20) return 8;
    if (count <= 60) return 5;
    return 3;
  }

  String _formatY(double v) {
    if (v >= 1e6) return '${(v / 1e6).toStringAsFixed(1)}M';
    if (v >= 1e3) return '${(v / 1e3).toStringAsFixed(1)}k';
    return v.toStringAsFixed(0);
  }

}
