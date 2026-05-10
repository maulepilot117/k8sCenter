// Tests for the chart-color contract. The plan's Mobile M4
// Banned-pattern rule says every fl_chart series color MUST resolve
// from `KubeColors`, not from a hardcoded literal. The shared
// `kubeChartSeverityColor` mapping is the only color path; verify
// each severity slot maps to the right token, and that the chart
// widget renders without exceptions for the documented inputs.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/kube_line_chart.dart';

void main() {
  group('kubeChartSeverityColor', () {
    final colors = buildKubeTheme('nexus').extension<KubeColors>()!;

    test('maps each severity to the corresponding KubeColors token', () {
      expect(kubeChartSeverityColor(KubeChartSeverity.primary, colors),
          colors.accent);
      expect(kubeChartSeverityColor(KubeChartSeverity.success, colors),
          colors.success);
      expect(kubeChartSeverityColor(KubeChartSeverity.warning, colors),
          colors.warning);
      expect(kubeChartSeverityColor(KubeChartSeverity.error, colors),
          colors.error);
      expect(kubeChartSeverityColor(KubeChartSeverity.info, colors),
          colors.info);
      expect(kubeChartSeverityColor(KubeChartSeverity.muted, colors),
          colors.textMuted);
    });

    test('all 6 severity slots resolve to distinct colors', () {
      // Catches a future regression where two severities collapse to
      // the same token (e.g. error and warning) — the chart contract
      // assumes each severity is visually distinct.
      final resolved = {
        for (final s in KubeChartSeverity.values)
          s: kubeChartSeverityColor(s, colors),
      };
      expect(resolved.values.toSet().length, KubeChartSeverity.values.length,
          reason: 'each severity must map to a distinct color token');
    });
  });

  group('KubeLineChart widget', () {
    testWidgets('renders no-data placeholder for an empty series list',
        (tester) async {
      await tester.pumpWidget(_wrap(const KubeLineChart(series: [])));
      expect(find.text('No data for this time range'), findsOneWidget);
    });

    testWidgets('renders no-data placeholder when every series has 0 points',
        (tester) async {
      await tester.pumpWidget(_wrap(const KubeLineChart(series: [
        (
          label: 'cpu',
          points: <MetricsPoint>[],
          severity: KubeChartSeverity.success,
        ),
      ])));
      expect(find.text('No data for this time range'), findsOneWidget);
    });

    testWidgets('renders the chart body when at least one series has points',
        (tester) async {
      final now = DateTime(2026, 5, 9, 12);
      await tester.pumpWidget(_wrap(KubeLineChart(series: [
        (
          label: 'cpu',
          points: [
            (t: now, v: 0.1),
            (t: now.add(const Duration(minutes: 1)), v: 0.2),
            (t: now.add(const Duration(minutes: 2)), v: 0.3),
          ],
          severity: KubeChartSeverity.success,
        ),
      ])));
      // No placeholder; chart rendered without exception.
      expect(find.text('No data for this time range'), findsNothing);
    });

    testWidgets('survives a single-point series without hanging on x-axis '
        'label computation (zero-range guard)', (tester) async {
      // The plan's reliability finding: when min == max, _xInterval
      // must return double.infinity so fl_chart renders at most one
      // axis label. A 1ms interval would attempt millions of labels
      // and hang the UI thread. We wrap in a fixed-size SizedBox so
      // fl_chart has bounded constraints during layout.
      final t = DateTime(2026, 5, 9, 12);
      await tester.pumpWidget(_wrap(SizedBox(
        width: 400,
        height: 240,
        child: KubeLineChart(series: [
          (
            label: 'cpu',
            points: [(t: t, v: 0.42)],
            severity: KubeChartSeverity.success,
          ),
        ]),
      )));
      // pumpWidget completes without hanging or throwing — the only
      // assertion the zero-range guard makes is "don't blow up".
      await tester.pump();
    });
  });
}

Widget _wrap(Widget child) {
  return MaterialApp(
    theme: buildKubeTheme('nexus'),
    home: Scaffold(body: child),
  );
}
