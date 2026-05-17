// Themed multi-series line chart used by the per-resource Metrics tab
// (PR-4b) and golden signals surface (PR-4e). All colors route through
// KubeColors so the chart adapts to the operator's active theme without
// any hardcoded hex literals.
//
// X-axis label strategy mirrors the web frontend: ranges ≤ 24 h show
// HH:mm; longer ranges show MM/dd HH:mm. This keeps the axis readable
// on a 360dp phone screen without truncation.
//
// PR-5f adds pinch-to-zoom on the X axis. A custom recognizer waits for
// 2+ pointers before claiming the gesture so a single-finger horizontal
// drag still propagates to the parent TabBarView and switches tabs
// cleanly. Double-tap resets the zoom to the data's natural range.
// Vertical pinch is rejected — charts do not zoom on Y in M5.

import 'package:fl_chart/fl_chart.dart';
import 'package:flutter/gestures.dart';
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
class KubeLineChart extends StatefulWidget {
  const KubeLineChart({
    required this.series,
    this.title,
    this.showGrid = true,
    this.showLegend = true,
    this.height = 200,
    this.enableZoom = true,
    super.key,
  });

  final List<MetricsSeries> series;
  final String? title;
  final bool showGrid;
  final bool showLegend;
  final double height;

  /// Opt-out for surfaces that don't want pinch-zoom (e.g. dashboard
  /// thumbnails). Default is on; non-data-bearing chart bodies skip
  /// the gesture wrapper entirely so the gesture arena stays empty.
  final bool enableZoom;

  @override
  State<KubeLineChart> createState() => KubeLineChartState();
}

/// Public for `@visibleForTesting` access to zoom state from widget
/// tests. Consumers should treat this as an opaque implementation
/// detail.
class KubeLineChartState extends State<KubeLineChart> {
  // Effective zoom window. Null pair = no zoom; the chart renders the
  // data's natural range. A non-null pair is the user's pinched window,
  // always clamped inside the current data's min/max so we never paint
  // an off-chart axis if data shrinks under a held zoom.
  double? _zoomMinX;
  double? _zoomMaxX;

  // Captured at scale gesture start so each onScaleUpdate computes
  // relative to the fingers-down range. Without this we'd compound
  // multiplicative scale onto whatever we already mutated mid-pinch
  // and the chart would runaway-zoom on any sustained gesture.
  double? _scaleStartMinX;
  double? _scaleStartMaxX;

  // Cached during the most recent build so gesture handlers can clamp
  // without recomputing point-min/max on every onScaleUpdate.
  double? _lastInitialMinX;
  double? _lastInitialMaxX;

  final GlobalKey _chartBoxKey = GlobalKey();

  /// Whether the user currently has a non-default zoom window applied.
  /// Test-only — production code reads the effective range through the
  /// chart's own rendering, not this flag.
  @visibleForTesting
  bool get isZoomed => _zoomMinX != null || _zoomMaxX != null;

  @visibleForTesting
  double? get zoomMinX => _zoomMinX;

  @visibleForTesting
  double? get zoomMaxX => _zoomMaxX;

  void _handleDoubleTap() {
    if (_zoomMinX == null && _zoomMaxX == null) return;
    setState(() {
      _zoomMinX = null;
      _zoomMaxX = null;
    });
  }

  void _handleScaleStart(ScaleStartDetails details) {
    _scaleStartMinX = _zoomMinX ?? _lastInitialMinX;
    _scaleStartMaxX = _zoomMaxX ?? _lastInitialMaxX;
  }

  void _handleScaleUpdate(ScaleUpdateDetails details) {
    final startMin = _scaleStartMinX;
    final startMax = _scaleStartMaxX;
    final initialMin = _lastInitialMinX;
    final initialMax = _lastInitialMaxX;
    if (startMin == null ||
        startMax == null ||
        initialMin == null ||
        initialMax == null) {
      return;
    }
    final startSpan = startMax - startMin;
    if (startSpan <= 0) return;

    // Horizontal axis only — vertical scaling is ignored per plan.
    // details.scale > 1 = pinch-out (zoom in → narrower span);
    // details.scale < 1 = pinch-in (zoom out → wider span).
    final scale = details.scale.clamp(0.1, 10.0);
    final newSpan = startSpan / scale;

    // Translate the focal point into chart-local x so the data value
    // under the user's pinch focus stays fixed while the visible
    // window narrows or widens around it.
    final box = _chartBoxKey.currentContext?.findRenderObject() as RenderBox?;
    double focalFraction = 0.5;
    if (box != null && box.hasSize && box.size.width > 0) {
      final localFocal = box.globalToLocal(details.focalPoint);
      focalFraction = (localFocal.dx / box.size.width).clamp(0.0, 1.0);
    }
    final focalDataX = startMin + focalFraction * startSpan;
    double newMin = focalDataX - focalFraction * newSpan;
    double newMax = focalDataX + (1 - focalFraction) * newSpan;

    // Slide the window if either edge is outside the initial range,
    // then clamp. This preserves the requested span when the user
    // zooms near a data boundary; clamping the edges alone would
    // silently change the zoom factor.
    if (newMin < initialMin) {
      final delta = initialMin - newMin;
      newMin += delta;
      newMax += delta;
    }
    if (newMax > initialMax) {
      final delta = newMax - initialMax;
      newMin -= delta;
      newMax -= delta;
    }
    newMin = newMin.clamp(initialMin, initialMax);
    newMax = newMax.clamp(initialMin, initialMax);
    if (newMax - newMin <= 0) return;

    // Snap to "fully zoomed out" when the user has effectively undone
    // the zoom — this lets the chart shed the null sentinels and
    // matches double-tap-reset semantics without a second explicit tap.
    final atInitialRange =
        (newMin - initialMin).abs() < 1e-6 &&
            (newMax - initialMax).abs() < 1e-6;

    setState(() {
      if (atInitialRange) {
        _zoomMinX = null;
        _zoomMaxX = null;
      } else {
        _zoomMinX = newMin;
        _zoomMaxX = newMax;
      }
    });
  }

  void _handleScaleEnd(ScaleEndDetails details) {
    _scaleStartMinX = null;
    _scaleStartMaxX = null;
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;

    // Pre-clean the series and compute the data's natural X range here
    // so the gesture handlers can clamp against a value the same build
    // pass derived. Doing it in _LineChartBody would force the handlers
    // to re-walk every point on each onScaleUpdate.
    final cleanedSeries = widget.series
        .map((s) => (
              label: s.label,
              points: _sortByTime(
                  s.points.where((p) => p.v.isFinite).toList()),
              severity: s.severity,
            ))
        .toList();
    final allPoints = cleanedSeries.expand((s) => s.points).toList();
    final hasData = allPoints.isNotEmpty;

    double? initialMinX;
    double? initialMaxX;
    if (hasData) {
      final minTs = allPoints.map((p) => p.t).reduce(
            (a, b) => a.isBefore(b) ? a : b,
          );
      final maxTs = allPoints.map((p) => p.t).reduce(
            (a, b) => a.isAfter(b) ? a : b,
          );
      // Pad zero-range axes (single-point series) — the SideTitlesWidget
      // OOM guard from the original implementation.
      final rawMinX = minTs.millisecondsSinceEpoch.toDouble();
      final rawMaxX = maxTs.millisecondsSinceEpoch.toDouble();
      initialMinX = rawMinX == rawMaxX ? rawMinX - 1 : rawMinX;
      initialMaxX = rawMinX == rawMaxX ? rawMaxX + 1 : rawMaxX;
    }
    _lastInitialMinX = initialMinX;
    _lastInitialMaxX = initialMaxX;

    // Clamp any held zoom into the current data's bounds so a refresh
    // that shrinks the dataset can't leave us pointing off-chart.
    final effectiveMinX = hasData
        ? (_zoomMinX != null
            ? _zoomMinX!.clamp(initialMinX!, initialMaxX!)
            : initialMinX!)
        : null;
    final effectiveMaxX = hasData
        ? (_zoomMaxX != null
            ? _zoomMaxX!.clamp(initialMinX!, initialMaxX!)
            : initialMaxX!)
        : null;

    Widget chartContent = SizedBox(
      height: widget.height,
      child: hasData
          ? _LineChartBody(
              key: _chartBoxKey,
              cleanedSeries: cleanedSeries,
              colors: colors,
              showGrid: widget.showGrid,
              minX: effectiveMinX!,
              maxX: effectiveMaxX!,
            )
          : _NoDataPlaceholder(colors: colors),
    );

    if (widget.enableZoom && hasData) {
      chartContent = RawGestureDetector(
        behavior: HitTestBehavior.opaque,
        gestures: <Type, GestureRecognizerFactory>{
          _TwoFingerScaleRecognizer:
              GestureRecognizerFactoryWithHandlers<_TwoFingerScaleRecognizer>(
            () => _TwoFingerScaleRecognizer(debugOwner: this),
            (recognizer) {
              recognizer
                ..onStart = _handleScaleStart
                ..onUpdate = _handleScaleUpdate
                ..onEnd = _handleScaleEnd;
            },
          ),
          DoubleTapGestureRecognizer:
              GestureRecognizerFactoryWithHandlers<DoubleTapGestureRecognizer>(
            () => DoubleTapGestureRecognizer(debugOwner: this),
            (recognizer) {
              recognizer.onDoubleTap = _handleDoubleTap;
            },
          ),
        },
        child: chartContent,
      );
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (widget.title != null) _ChartTitle(title: widget.title!, colors: colors),
        chartContent,
        if (widget.showLegend && hasData)
          _Legend(series: widget.series, colors: colors),
      ],
    );
  }

  /// Sorts points by timestamp ascending. fl_chart connects spots in
  /// array order — out-of-order timestamps (Prometheus federated merge,
  /// Loki shard interleave) render as visible "backwards" segments.
  List<MetricsPoint> _sortByTime(List<MetricsPoint> points) {
    if (points.length <= 1) return points;
    final sorted = [...points]..sort((a, b) => a.t.compareTo(b.t));
    return sorted;
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
          final color = kubeChartSeverityColor(s.severity, colors);
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
    super.key,
    required this.cleanedSeries,
    required this.colors,
    required this.showGrid,
    required this.minX,
    required this.maxX,
  });

  final List<MetricsSeries> cleanedSeries;
  final KubeColors colors;
  final bool showGrid;
  final double minX;
  final double maxX;

  @override
  Widget build(BuildContext context) {
    // Re-derive an effective Y range from points inside the visible
    // window so a zoom-in re-fits Y to the local extrema. If the window
    // contains no points (zoomed into a gap between samples) fall back
    // to the full-dataset Y so the chart doesn't collapse to a line.
    final allPoints = cleanedSeries.expand((s) => s.points).toList();
    final visiblePoints = allPoints.where((p) {
      final tx = p.t.millisecondsSinceEpoch.toDouble();
      return tx >= minX && tx <= maxX;
    }).toList();
    final yPoints = visiblePoints.isEmpty ? allPoints : visiblePoints;

    final maxTs = DateTime.fromMillisecondsSinceEpoch(maxX.toInt());
    final minTs = DateTime.fromMillisecondsSinceEpoch(minX.toInt());
    final rangeHours = maxTs.difference(minTs).inHours;
    // Short ranges: show HH:mm; longer ranges: include date.
    final labelFmt = rangeHours <= 24
        ? DateFormat('HH:mm')
        : DateFormat('MM/dd HH:mm');

    final yValues = yPoints.map((p) => p.v);
    final rawMinY = yValues.reduce((a, b) => a < b ? a : b);
    final rawMaxY = yValues.reduce((a, b) => a > b ? a : b);
    final minY = rawMinY == rawMaxY ? rawMinY - 1 : rawMinY;
    final maxY = rawMinY == rawMaxY ? rawMaxY + 1 : rawMaxY;

    final barData = cleanedSeries.map((s) {
      final color = kubeChartSeverityColor(s.severity, colors);
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
        minX: minX,
        maxX: maxX,
        minY: minY,
        maxY: maxY,
        clipData: const FlClipData.all(),
        // fl_chart's default touch handlers attach a PanGestureRecognizer
        // for tooltip dragging — that recognizer competes with our
        // two-finger scale recognizer over single-pointer events and
        // wins because fl_chart's child gesture detector resolves first.
        // Disabling internal touch lets the parent gesture-arena gate
        // single-finger drags through to TabBarView and two-finger
        // pinches into our scale handlers without contention.
        lineTouchData: const LineTouchData(enabled: false),
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
              interval: (maxX - minX) / 4,
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
              interval: (maxY - minY) / 4,
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

  String _formatY(double v) {
    if (v >= 1e9) return '${(v / 1e9).toStringAsFixed(1)}G';
    if (v >= 1e6) return '${(v / 1e6).toStringAsFixed(1)}M';
    if (v >= 1e3) return '${(v / 1e3).toStringAsFixed(1)}k';
    return v.toStringAsFixed(v.truncate() == v ? 0 : 1);
  }
}

/// Two-finger scale recognizer. Rejects the gesture while only one
/// pointer is down so the parent `TabBarView`'s horizontal-drag
/// recognizer can win single-finger swipes cleanly. Without this gate
/// fl_chart's chart area would swallow every drag and tab swipes would
/// stop working anywhere a chart was visible.
class _TwoFingerScaleRecognizer extends ScaleGestureRecognizer {
  _TwoFingerScaleRecognizer({super.debugOwner});

  @override
  void acceptGesture(int pointer) {
    if (pointerCount >= 2) {
      super.acceptGesture(pointer);
    } else {
      rejectGesture(pointer);
    }
  }
}

/// Maps a [KubeChartSeverity] to the corresponding [KubeColors] token.
/// Kept as a top-level function so both the line chart and bar chart
/// can share the same mapping without duplication.
Color kubeChartSeverityColor(KubeChartSeverity severity, KubeColors colors) {
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
