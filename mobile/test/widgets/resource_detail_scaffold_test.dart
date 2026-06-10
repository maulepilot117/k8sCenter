// Backwards-compat tests for the M4 PR-4a `extraTabs` parameter on
// `ResourceDetailScaffold`. Two invariants matter:
//
//   1. With no extraTabs passed, the scaffold renders the canonical
//      Overview / YAML / Events triplet exactly as M1/M2/M3 ship today.
//      Length-mismatch with `DefaultTabController` would throw.
//   2. With extraTabs passed, length is `3 + extras.length`, the TabBar
//      becomes scrollable, and the extra labels + bodies render.
//
// These two cases together prove the wire-up of every PR-4b+ caller.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/resource_detail_scaffold.dart';

void main() {
  group('ResourceDetailScaffold extraTabs', () {
    testWidgets('default 3-tab layout when extraTabs is empty (backwards-compat)',
        (tester) async {
      await _pump(tester, extraTabs: const []);
      expect(find.text('Overview'), findsOneWidget);
      expect(find.text('YAML'), findsOneWidget);
      expect(find.text('Events'), findsOneWidget);
      // Default 3-tab layout uses non-scrollable TabBar.
      final tabBar = tester.widget<TabBar>(find.byType(TabBar));
      expect(tabBar.isScrollable, isFalse);
    });

    testWidgets('appends extra tabs after Events; TabBar becomes scrollable',
        (tester) async {
      const extra = [
        DetailExtraTab(
            label: 'Metrics', body: Center(child: Text('metrics-body'))),
        DetailExtraTab(
            label: 'Logs', body: Center(child: Text('logs-body'))),
      ];
      await _pump(tester, extraTabs: extra);
      expect(find.text('Overview'), findsOneWidget);
      expect(find.text('Metrics'), findsOneWidget);
      expect(find.text('Logs'), findsOneWidget);
      final tabBar = tester.widget<TabBar>(find.byType(TabBar));
      expect(tabBar.isScrollable, isTrue,
          reason: 'TabBar must be scrollable when extraTabs are present so '
              'tabs do not clip on phone-sized AppBar widths');
      // Tap the Metrics tab and verify the body renders.
      await tester.tap(find.text('Metrics'));
      await tester.pumpAndSettle();
      expect(find.text('metrics-body'), findsOneWidget);
    });
  });
}

Future<void> _pump(
  WidgetTester tester, {
  required List<DetailExtraTab> extraTabs,
}) async {
  await tester.pumpWidget(ProviderScope(
    child: MaterialApp(
      theme: buildKubeTheme('liquid-glass'),
      home: ResourceDetailScaffold(
        kindLabel: 'Pod',
        name: 'test-pod',
        namespace: 'default',
        resource: const <String, dynamic>{
          'metadata': {'name': 'test-pod', 'namespace': 'default'},
        },
        overview: const Center(child: Text('overview-body')),
        extraTabs: extraTabs,
      ),
    ),
  ));
  // Allow the EventsTab's resourceListProvider to settle (loading
  // state — no fetch fires because no Dio override is configured;
  // EventsTab renders a LoadingState which is fine for this test).
  await tester.pump();
}
