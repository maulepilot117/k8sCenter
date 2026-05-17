// Donut gauge used for compliance scores (PR-4h policy, PR-4i scanning)
// and ESO drift summaries (PR-4g). CustomPaint is used instead of
// fl_chart's PieChart because PieChart doesn't expose a clean API for
// the thick-stroke / thin-background-ring donut UX — the result would
// require hacking around its internal padding and clipping math.
//
// severityForPercentage is a top-level helper so callers can compute
// the arc color from a 0–1 ratio without constructing a widget.

import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';
import 'kube_line_chart.dart' show KubeChartSeverity, kubeChartSeverityColor;

/// Returns the appropriate chart severity for a 0–1 percentage.
/// Thresholds are configurable so callers can tune warn/crit per domain.
KubeChartSeverity severityForPercentage(
  double pct, {
  double warnBelow = 0.9,
  double critBelow = 0.7,
}) {
  if (pct >= warnBelow) return KubeChartSeverity.success;
  if (pct >= critBelow) return KubeChartSeverity.warning;
  return KubeChartSeverity.error;
}

/// Circular donut gauge with a themed arc and center label.
///
/// The filled arc spans `percentage * 2π` starting from the 12 o'clock
/// position (−π/2). The background ring is drawn first in
/// [KubeColors.bgElevated] to fill the remaining arc, giving a
/// continuous ring appearance.
class KubeGaugeRing extends StatelessWidget {
  const KubeGaugeRing({
    required this.percentage,
    required this.centerLabel,
    this.subtitle,
    this.severity = KubeChartSeverity.success,
    this.size = 120,
    this.semanticsLabel,
    super.key,
  });

  /// Fraction filled, clamped to [0.0, 1.0].
  final double percentage;

  /// Large bold text in the centre (e.g. "87%").
  final String centerLabel;

  /// Optional small muted text below [centerLabel].
  final String? subtitle;

  final KubeChartSeverity severity;

  /// Outer diameter of the ring widget in logical pixels.
  final double size;

  /// Optional accessibility label announced to screen readers. When null,
  /// defaults to "$centerLabel${subtitle != null ? ', $subtitle' : ''}".
  final String? semanticsLabel;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final fillColor = kubeChartSeverityColor(severity, colors);

    // Guard non-finite percentages (e.g. 0/0 from a "0 of 0 policies
    // evaluated" compliance score). NaN.clamp(0,1) is NaN; the painter
    // would render a 0% empty ring while the centerLabel showed "NaN%".
    // Substitute a 0 fill so the operator sees an empty ring with
    // whatever no-data copy the caller supplies in centerLabel.
    final safePct = percentage.isFinite ? percentage.clamp(0.0, 1.0) : 0.0;
    final resolvedLabel = semanticsLabel ??
        '$centerLabel${subtitle != null ? ', $subtitle' : ''}';

    return Semantics(
      label: resolvedLabel,
      excludeSemantics: true,
      child: SizedBox(
      width: size,
      height: size,
      child: CustomPaint(
        painter: _GaugePainter(
          percentage: safePct,
          fillColor: fillColor,
          trackColor: colors.bgElevated,
          strokeWidth: 12,
        ),
        child: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                centerLabel,
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: size * 0.18,
                  fontWeight: FontWeight.bold,
                  height: 1.1,
                ),
              ),
              if (subtitle != null)
                Text(
                  subtitle!,
                  style: TextStyle(
                    color: colors.textMuted,
                    fontSize: size * 0.11,
                    height: 1.2,
                  ),
                  textAlign: TextAlign.center,
                ),
            ],
          ),
        ),
      ),
    ),
    );
  }

}

// ---------------------------------------------------------------------------
// Painter
// ---------------------------------------------------------------------------

class _GaugePainter extends CustomPainter {
  _GaugePainter({
    required this.percentage,
    required this.fillColor,
    required this.trackColor,
    required this.strokeWidth,
  });

  final double percentage;
  final Color fillColor;
  final Color trackColor;
  final double strokeWidth;

  @override
  void paint(Canvas canvas, Size size) {
    final center = Offset(size.width / 2, size.height / 2);
    final radius = (size.shortestSide / 2) - (strokeWidth / 2);

    final trackPaint = Paint()
      ..color = trackColor
      ..strokeWidth = strokeWidth
      ..style = PaintingStyle.stroke
      ..strokeCap = StrokeCap.round;

    final fillPaint = Paint()
      ..color = fillColor
      ..strokeWidth = strokeWidth
      ..style = PaintingStyle.stroke
      ..strokeCap = StrokeCap.round;

    final rect = Rect.fromCircle(center: center, radius: radius);

    // Background full ring.
    canvas.drawArc(rect, 0, 2 * math.pi, false, trackPaint);

    // Filled arc: start at 12 o'clock (−π/2), sweep clockwise.
    if (percentage > 0) {
      canvas.drawArc(
        rect,
        -math.pi / 2,
        2 * math.pi * percentage,
        false,
        fillPaint,
      );
    }
  }

  @override
  bool shouldRepaint(_GaugePainter old) =>
      !_pctEquals(old.percentage, percentage) ||
      old.fillColor != fillColor ||
      old.trackColor != trackColor ||
      old.strokeWidth != strokeWidth;

  /// NaN-aware equality. `NaN != NaN` in Dart, which would force a
  /// repaint on every rebuild for a non-finite percentage.
  static bool _pctEquals(double a, double b) {
    if (a.isNaN && b.isNaN) return true;
    return a == b;
  }
}
