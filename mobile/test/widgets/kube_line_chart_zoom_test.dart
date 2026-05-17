// Tests for PR-5f chart zoom on KubeLineChart.
//
// The recognizer-level gate must reject single-finger horizontal
// drags so a parent TabBarView can still swipe between tabs from the
// chart area. Pinching with 2 pointers narrows or widens the X
// window. Double-tap returns to the data's natural range.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/kube_line_chart.dart';

void main() {
  final t0 = DateTime(2026, 5, 9, 12);
  final samplePoints = List.generate(
    20,
    (i) => (t: t0.add(Duration(minutes: i)), v: (i + 1).toDouble()),
  );
  final sampleSeries = [
    (
      label: 'cpu',
      points: samplePoints,
      severity: KubeChartSeverity.success,
    ),
  ];

  testWidgets('initial state has no zoom applied', (tester) async {
    await tester.pumpWidget(_wrap(KubeLineChart(series: sampleSeries)));
    final state = tester.state<KubeLineChartState>(find.byType(KubeLineChart));
    expect(state.isZoomed, isFalse);
    expect(state.zoomMinX, isNull);
    expect(state.zoomMaxX, isNull);
  });

  testWidgets('two-finger pinch-out narrows the X range', (tester) async {
    await tester.pumpWidget(_wrap(SizedBox(
      width: 400,
      height: 240,
      child: KubeLineChart(series: sampleSeries),
    )));
    final state = tester.state<KubeLineChartState>(find.byType(KubeLineChart));
    expect(state.isZoomed, isFalse);

    final chartCenter = tester.getCenter(find.byType(KubeLineChart));
    final left = chartCenter + const Offset(-40, 0);
    final right = chartCenter + const Offset(40, 0);
    final gesture1 = await tester.createGesture();
    final gesture2 = await tester.createGesture();
    await gesture1.down(left);
    await gesture2.down(right);
    await tester.pump();
    // Pull fingers further apart → pinch-out → zoom in (narrower span).
    await gesture1.moveTo(chartCenter + const Offset(-160, 0));
    await gesture2.moveTo(chartCenter + const Offset(160, 0));
    await tester.pump();
    await gesture1.up();
    await gesture2.up();
    await tester.pump();

    expect(state.isZoomed, isTrue,
        reason: 'Pinch-out should establish a non-null zoom window');
    expect(state.zoomMaxX! - state.zoomMinX!, lessThan(
        samplePoints.last.t.millisecondsSinceEpoch.toDouble() -
            samplePoints.first.t.millisecondsSinceEpoch.toDouble()),
        reason: 'Zoomed span must be tighter than the data\'s initial span');
    // Flush the DoubleTapGestureRecognizer's settle timer before
    // teardown — every pointer-down registers a candidate tap and the
    // recognizer holds it for kDoubleTapTimeout looking for a second
    // tap that will never arrive.
    await tester.pump(const Duration(milliseconds: 500));
  });

  testWidgets('double-tap resets a held zoom', (tester) async {
    await tester.pumpWidget(_wrap(SizedBox(
      width: 400,
      height: 240,
      child: KubeLineChart(series: sampleSeries),
    )));
    final state = tester.state<KubeLineChartState>(find.byType(KubeLineChart));

    // Apply a zoom first.
    final chartCenter = tester.getCenter(find.byType(KubeLineChart));
    final gesture1 = await tester.createGesture();
    final gesture2 = await tester.createGesture();
    await gesture1.down(chartCenter + const Offset(-40, 0));
    await gesture2.down(chartCenter + const Offset(40, 0));
    await tester.pump();
    await gesture1.moveTo(chartCenter + const Offset(-150, 0));
    await gesture2.moveTo(chartCenter + const Offset(150, 0));
    await tester.pump();
    await gesture1.up();
    await gesture2.up();
    await tester.pump();
    expect(state.isZoomed, isTrue);

    // Now double-tap to reset.
    await tester.tap(find.byType(KubeLineChart));
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.byType(KubeLineChart));
    await tester.pump(const Duration(milliseconds: 50));

    expect(state.isZoomed, isFalse,
        reason: 'Double-tap should clear the zoom window');
    expect(state.zoomMinX, isNull);
    expect(state.zoomMaxX, isNull);
  });

  testWidgets(
      'single-finger horizontal drag does NOT zoom (recognizer gate)',
      (tester) async {
    await tester.pumpWidget(_wrap(SizedBox(
      width: 400,
      height: 240,
      child: KubeLineChart(series: sampleSeries),
    )));
    final state = tester.state<KubeLineChartState>(find.byType(KubeLineChart));

    final chartCenter = tester.getCenter(find.byType(KubeLineChart));
    final gesture = await tester.startGesture(chartCenter);
    await gesture.moveBy(const Offset(120, 0));
    await tester.pump();
    await gesture.up();
    await tester.pump();

    expect(state.isZoomed, isFalse,
        reason:
            'Single-finger drag must not be claimed by the scale recognizer');
    await tester.pump(const Duration(milliseconds: 500));
  });

  testWidgets('enableZoom: false skips the gesture wrapper', (tester) async {
    await tester.pumpWidget(_wrap(SizedBox(
      width: 400,
      height: 240,
      child: KubeLineChart(series: sampleSeries, enableZoom: false),
    )));
    final state = tester.state<KubeLineChartState>(find.byType(KubeLineChart));

    final chartCenter = tester.getCenter(find.byType(KubeLineChart));
    final gesture1 = await tester.createGesture();
    final gesture2 = await tester.createGesture();
    await gesture1.down(chartCenter + const Offset(-40, 0));
    await gesture2.down(chartCenter + const Offset(40, 0));
    await tester.pump();
    await gesture1.moveTo(chartCenter + const Offset(-160, 0));
    await gesture2.moveTo(chartCenter + const Offset(160, 0));
    await tester.pump();
    await gesture1.up();
    await gesture2.up();
    await tester.pump();

    expect(state.isZoomed, isFalse,
        reason: 'enableZoom: false must not produce any zoom state');
    await tester.pump(const Duration(milliseconds: 500));
  });

  testWidgets('pinch-in past the initial range stays clamped',
      (tester) async {
    await tester.pumpWidget(_wrap(SizedBox(
      width: 400,
      height: 240,
      child: KubeLineChart(series: sampleSeries),
    )));
    final state = tester.state<KubeLineChartState>(find.byType(KubeLineChart));

    final chartCenter = tester.getCenter(find.byType(KubeLineChart));
    final gesture1 = await tester.createGesture();
    final gesture2 = await tester.createGesture();
    await gesture1.down(chartCenter + const Offset(-160, 0));
    await gesture2.down(chartCenter + const Offset(160, 0));
    await tester.pump();
    // Pinch fingers together aggressively past the initial range —
    // the clamp should snap _zoomMinX/_zoomMaxX back to null rather
    // than holding a window wider than the data.
    await gesture1.moveTo(chartCenter + const Offset(-2, 0));
    await gesture2.moveTo(chartCenter + const Offset(2, 0));
    await tester.pump();
    await gesture1.up();
    await gesture2.up();
    await tester.pump();

    final initialMin =
        samplePoints.first.t.millisecondsSinceEpoch.toDouble();
    final initialMax =
        samplePoints.last.t.millisecondsSinceEpoch.toDouble();
    if (state.isZoomed) {
      expect(state.zoomMinX, greaterThanOrEqualTo(initialMin));
      expect(state.zoomMaxX, lessThanOrEqualTo(initialMax));
    }
    // Either we snapped back to "not zoomed" (preferred) or we're
    // clamped strictly inside the data range — both satisfy the
    // invariant "never paint axis off the data".
    await tester.pump(const Duration(milliseconds: 500));
  });
}

Widget _wrap(Widget child) {
  return MaterialApp(
    theme: buildKubeTheme('nexus'),
    home: Scaffold(body: Center(child: child)),
  );
}
