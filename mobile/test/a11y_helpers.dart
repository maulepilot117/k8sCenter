// Shared accessibility-assertion helpers for `mobile/test/`.
//
// PR-5h (M5 a11y pass) ports every M1–M4 screen through these so contrast,
// tap-target, and traversal coverage stays uniform. The helpers wrap
// `flutter_test`'s `meetsGuideline` API so failures attribute back to the
// specific guideline that broke rather than a raw matcher mismatch.

import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter_test/flutter_test.dart';

/// Asserts the currently-pumped widget tree satisfies the platform a11y
/// guidelines specified by the flags. All flags default to `true`.
///
/// Each guideline assertion is independent — a contrast failure does not
/// short-circuit the tap-target check, so a single test surfaces every
/// guideline broken by the tree in one run.
///
/// Usage:
/// ```dart
/// testWidgets('Login screen meets a11y guidelines', (tester) async {
///   await tester.pumpWidget(buildHarness());
///   await expectMeetsAllGuidelines(tester);
/// });
/// ```
Future<void> expectMeetsAllGuidelines(
  WidgetTester tester, {
  bool textContrast = true,
  bool iOSTapTarget = true,
  bool androidTapTarget = true,
  bool labeledTapTargets = true,
}) async {
  final handle = tester.ensureSemantics();
  try {
    if (textContrast) {
      await expectLater(tester, meetsGuideline(textContrastGuideline));
    }
    if (iOSTapTarget) {
      await expectLater(tester, meetsGuideline(iOSTapTargetGuideline));
    }
    if (androidTapTarget) {
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
    }
    if (labeledTapTargets) {
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
    }
  } finally {
    handle.dispose();
  }
}

/// Convenience builder that wraps an arbitrary widget in the minimal
/// scaffolding (MediaQuery + Directionality + Material/Theme) needed
/// for `meetsGuideline(textContrastGuideline)` to compute contrast
/// pairs correctly.
///
/// Pass the theme you want to verify against — PR-5h tests iterate
/// through all 7 generated KubeColors themes.
Widget a11yHarness({
  required ThemeData theme,
  required Widget child,
  Size size = const Size(412, 915), // Pixel 6 logical pixels
  double textScaler = 1.0,
}) {
  return MediaQuery(
    data: MediaQueryData(
      size: size,
      textScaler: TextScaler.linear(textScaler),
    ),
    child: Directionality(
      textDirection: TextDirection.ltr,
      child: MaterialApp(
        theme: theme,
        debugShowCheckedModeBanner: false,
        home: Scaffold(body: child),
      ),
    ),
  );
}

/// Returns the semantics node for the widget matching [finder], failing
/// the test if there isn't exactly one match. Useful for asserting
/// `semanticsLabel`, `tooltip`, and traversal order on a specific
/// affordance.
SemanticsNode findSemanticsFor(WidgetTester tester, Finder finder) {
  expect(finder, findsOneWidget, reason: 'a11y assertion target not unique');
  return tester.getSemantics(finder);
}
