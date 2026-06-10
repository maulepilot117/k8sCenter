// GlassContainer: blur path, high-contrast solid fallback, and the
// elevated/surface fill branches. The fallback branch is otherwise
// unreachable in widget tests (no platform accessibility flag), so it is
// exercised here via an explicit MediaQuery override.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/glass_container.dart';

void main() {
  final colors =
      buildKubeTheme('liquid-glass').extension<KubeColors>()!;

  Widget host({required bool highContrast, bool elevated = true}) {
    return MaterialApp(
      theme: buildKubeTheme('liquid-glass'),
      home: MediaQuery(
        data: MediaQueryData(highContrast: highContrast),
        child: Scaffold(
          body: Center(
            child: GlassContainer(
              elevated: elevated,
              child: const Text('content'),
            ),
          ),
        ),
      ),
    );
  }

  Color? fillOf(WidgetTester tester) {
    final container = tester.widget<Container>(
      find.descendant(
        of: find.byType(GlassContainer),
        matching: find.byType(Container),
      ),
    );
    return (container.decoration as BoxDecoration?)?.color;
  }

  testWidgets('normal path renders BackdropFilter with glassElevated fill',
      (tester) async {
    await tester.pumpWidget(host(highContrast: false));

    expect(find.byType(BackdropFilter), findsOneWidget);
    expect(fillOf(tester), colors.glassElevated);
  });

  testWidgets('elevated: false uses the glassSurface fill', (tester) async {
    await tester.pumpWidget(host(highContrast: false, elevated: false));

    expect(fillOf(tester), colors.glassSurface);
  });

  testWidgets('high contrast drops the blur and falls back to solid',
      (tester) async {
    await tester.pumpWidget(host(highContrast: true));

    expect(find.byType(BackdropFilter), findsNothing);
    expect(fillOf(tester), colors.bgElevated);
  });
}
