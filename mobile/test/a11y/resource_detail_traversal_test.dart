// ResourceDetailScaffold traversal test — pumps the scaffold with mock
// data and asserts a screen-reader walks: back button → resource title
// (kind + namespace) → tabs in declared order. Mirrors WCAG 2.4.3 intent
// — the detail screen's affordances must be reachable in a meaningful
// top-to-bottom + left-to-right sequence.
//
// We render a cluster-scoped kind (no namespace) so the optional
// Diagnose action never appears and the test stays focused on the
// canonical chrome the scaffold ships for every kind.

import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/resource_detail_scaffold.dart';

import '../a11y_helpers.dart';

Widget _harness({List<DetailExtraTab> extraTabs = const []}) {
  return ProviderScope(
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: ResourceDetailScaffold(
        kindLabel: 'PersistentVolume',
        name: 'test-pv',
        statusLabel: 'Bound',
        resource: const {
          'apiVersion': 'v1',
          'kind': 'PersistentVolume',
          'metadata': {'name': 'test-pv'},
        },
        overview: const Text('overview body'),
        extraTabs: extraTabs,
      ),
    ),
  );
}

void main() {
  testWidgets('canonical 3-tab traversal: back → title → Overview → YAML → Events',
      (tester) async {
    await tester.pumpWidget(_harness());
    await tester.pumpAndSettle();

    final handle = tester.ensureSemantics();

    // Back button is present and exposes the canonical "Back" label
    // (tooltip doubles as the semantics label per Flutter convention).
    final back = find.widgetWithIcon(IconButton, Icons.arrow_back);
    expect(back, findsOneWidget);
    final backNode = findSemanticsFor(tester, back);
    expect(backNode.getSemanticsData().tooltip, 'Back',
        reason: 'Back IconButton must carry the Back tooltip');
    expect(backNode.getSemanticsData().hasAction(SemanticsAction.tap), isTrue);

    // Title chrome.
    expect(find.text('test-pv'), findsOneWidget);
    expect(find.text('PersistentVolume'), findsOneWidget);

    // Three canonical tabs visible.
    expect(find.text('Overview'), findsOneWidget);
    expect(find.text('YAML'), findsOneWidget);
    expect(find.text('Events'), findsOneWidget);

    // Status pill announces with its domain prefix.
    expect(
      find.bySemanticsLabel('Status: Bound'),
      findsOneWidget,
      reason: 'Status pill must announce with the "Status:" prefix',
    );

    // The traversal-order signal on a tabbed scaffold: vertical order
    // is back/title above tab bar; horizontal order within the tab bar
    // is Overview < YAML < Events. Assertion uses widget positions
    // because semantics traversal in Flutter mirrors hit-test order,
    // which is determined by paint order, which mirrors layout order.
    final backY = tester.getTopLeft(back).dy;
    final titleY = tester.getTopLeft(find.text('test-pv')).dy;
    final overviewX = tester.getTopLeft(find.text('Overview')).dx;
    final yamlX = tester.getTopLeft(find.text('YAML')).dx;
    final eventsX = tester.getTopLeft(find.text('Events')).dx;
    final overviewY = tester.getTopLeft(find.text('Overview')).dy;

    expect(backY, lessThanOrEqualTo(titleY),
        reason: 'back must sit at or above title row');
    expect(titleY, lessThan(overviewY),
        reason: 'title must traverse before the tab bar');
    expect(overviewX, lessThan(yamlX),
        reason: 'Overview tab must precede YAML tab');
    expect(yamlX, lessThan(eventsX),
        reason: 'YAML tab must precede Events tab');

    handle.dispose();
  });

  testWidgets('extra tabs render after Events in declaration order',
      (tester) async {
    await tester.pumpWidget(_harness(extraTabs: const [
      DetailExtraTab(label: 'Metrics', body: Text('metrics body')),
      DetailExtraTab(label: 'Logs', body: Text('logs body')),
    ]));
    await tester.pumpAndSettle();

    final handle = tester.ensureSemantics();

    expect(find.text('Overview'), findsOneWidget);
    expect(find.text('YAML'), findsOneWidget);
    expect(find.text('Events'), findsOneWidget);
    expect(find.text('Metrics'), findsOneWidget);
    expect(find.text('Logs'), findsOneWidget);

    final eventsX = tester.getTopLeft(find.text('Events')).dx;
    final metricsX = tester.getTopLeft(find.text('Metrics')).dx;
    final logsX = tester.getTopLeft(find.text('Logs')).dx;
    expect(eventsX, lessThan(metricsX),
        reason: 'Events must precede the first extra tab');
    expect(metricsX, lessThan(logsX),
        reason: 'Metrics must precede Logs (declaration order)');

    handle.dispose();
  });
}
