// Verifies the 768px breakpoint switches between phone (single-pane) and
// tablet (two-pane master-detail) layouts.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/adaptive_scaffold.dart';

Widget _harness({
  required Widget child,
  required Size size,
}) {
  return MaterialApp(
    theme: buildKubeTheme('liquid-glass'),
    home: MediaQuery(
      data: MediaQueryData(size: size),
      child: SizedBox.fromSize(
        size: size,
        child: child,
      ),
    ),
  );
}

void main() {
  testWidgets('phone width renders single-pane (no detail visible)',
      (tester) async {
    tester.view.physicalSize = const Size(390 * 3, 800 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      _harness(
        size: const Size(390, 800),
        child: const AdaptiveScaffold(
          title: 'phone',
          body: Center(child: Text('list')),
          detail: Center(child: Text('detail')),
        ),
      ),
    );

    expect(find.text('list'), findsOneWidget);
    expect(find.text('detail'), findsNothing);
  });

  testWidgets('tablet width renders two-pane (both list and detail visible)',
      (tester) async {
    tester.view.physicalSize = const Size(900 * 2, 700 * 2);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      _harness(
        size: const Size(900, 700),
        child: const AdaptiveScaffold(
          title: 'tablet',
          body: Center(child: Text('list')),
          detail: Center(child: Text('detail')),
        ),
      ),
    );

    expect(find.text('list'), findsOneWidget);
    expect(find.text('detail'), findsOneWidget);
  });

  testWidgets('breakpoint at exactly 768 shows two-pane (>= boundary)',
      (tester) async {
    tester.view.physicalSize = const Size(768 * 2, 1024 * 2);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      _harness(
        size: const Size(768, 1024),
        child: const AdaptiveScaffold(
          title: 'edge',
          body: Center(child: Text('list')),
          detail: Center(child: Text('detail')),
        ),
      ),
    );

    expect(find.text('list'), findsOneWidget);
    expect(find.text('detail'), findsOneWidget);
  });
}
