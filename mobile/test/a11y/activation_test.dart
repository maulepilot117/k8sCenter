// Regression coverage for the PR-5h post-review fix: any widget that
// pairs `Semantics(button: true, excludeSemantics: true, ...)` with a
// child `InkWell` / `ListTile` whose `onTap` is the canonical activation
// must also expose `onTap` on the outer Semantics. Without it,
// `excludeSemantics: true` strips the descendant's tap action from the
// accessibility tree and screen-reader double-tap does nothing — the
// wrapper announces "button" but no SemanticsAction.tap is published.
//
// `ClusterPill` is the public reference case; identical fixes were
// applied to `_NotificationTile` (feed_screen.dart) and `_RevisionTile`
// (rollback_picker_screen.dart). Those widgets are private to their
// feature directories; the canonical pattern is exercised here, and the
// per-screen feature tests (notifications_feed_test.dart,
// rollback_picker_test.dart) cover the rest.

import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/cluster_pill.dart';

Widget _harness(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: Scaffold(body: Center(child: child)),
    ),
  );
}

void main() {
  testWidgets('ClusterPill outer Semantics exposes SemanticsAction.tap',
      (tester) async {
    await tester.pumpWidget(_harness(const ClusterPill()));
    await tester.pumpAndSettle();

    final handle = tester.ensureSemantics();
    final pill = find.byKey(const ValueKey('cluster-pill'));
    expect(pill, findsOneWidget);
    // The merged semantics node at the InkWell location includes the
    // outer Semantics's properties (label, button) AND any actions it
    // exposes. The post-review fix added onTap on the outer Semantics
    // so SemanticsAction.tap survives excludeSemantics: true.
    final data = tester.getSemantics(pill).getSemanticsData();
    expect(data.hasAction(SemanticsAction.tap), isTrue,
        reason:
            'ClusterPill must publish SemanticsAction.tap so screen-reader '
            'users can double-tap to open the cluster picker. '
            'Regression check for the PR-5h post-review fix: a '
            'Semantics(excludeSemantics: true, child: InkWell) wrapper '
            'without an onTap argument on the wrapper itself silently '
            'drops the InkWell tap from the a11y tree.');
    handle.dispose();
  });
}
