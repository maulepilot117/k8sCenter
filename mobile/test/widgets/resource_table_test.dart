// ResourceTable widget tests: phone vs tablet layout, tap callback,
// empty state, and per-row color override.

import 'package:data_table_2/data_table_2.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/resource_table.dart';

class _Row {
  _Row(this.name, this.status);
  final String name;
  final String status;
}

Widget _harness({required Widget child, required Size size}) {
  return MaterialApp(
    theme: buildKubeTheme('liquid-glass'),
    home: MediaQuery(
      data: MediaQueryData(size: size),
      child: SizedBox.fromSize(size: size, child: child),
    ),
  );
}

void main() {
  testWidgets('phone layout renders ListTile cards with tap callback',
      (tester) async {
    tester.view.physicalSize = const Size(390 * 3, 800 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    _Row? tapped;
    await tester.pumpWidget(_harness(
      size: const Size(390, 800),
      child: Scaffold(
        body: ResourceTable<_Row>(
          items: [
            _Row('alpha', 'Running'),
            _Row('beta', 'Pending'),
          ],
          columns: [
            ResourceColumn(label: 'Name', value: (r) => r.name),
            ResourceColumn(label: 'Status', value: (r) => r.status),
          ],
          onTap: (r) => tapped = r,
        ),
      ),
    ));
    await tester.pumpAndSettle();

    expect(find.byType(ListTile), findsNWidgets(2));
    expect(find.byType(PaginatedDataTable2), findsNothing);

    await tester.tap(find.byKey(const ValueKey('resource-row-1')));
    await tester.pumpAndSettle();
    expect(tapped?.name, 'beta');
  });

  testWidgets('tablet layout renders PaginatedDataTable2', (tester) async {
    tester.view.physicalSize = const Size(900 * 2, 700 * 2);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(_harness(
      size: const Size(900, 700),
      child: Scaffold(
        body: ResourceTable<_Row>(
          items: [_Row('alpha', 'Running')],
          columns: [
            ResourceColumn(label: 'Name', value: (r) => r.name),
            ResourceColumn(label: 'Status', value: (r) => r.status),
          ],
          onTap: (_) {},
        ),
      ),
    ));
    await tester.pumpAndSettle();

    expect(find.byType(PaginatedDataTable2), findsOneWidget);
    expect(find.byType(ListTile), findsNothing);
    // The cell text should be rendered through the source's getRow path.
    expect(find.text('alpha'), findsOneWidget);
  });

  testWidgets('empty list shows "No resources found"', (tester) async {
    await tester.pumpWidget(_harness(
      size: const Size(390, 800),
      child: Scaffold(
        body: ResourceTable<_Row>(
          items: const [],
          columns: [
            ResourceColumn(label: 'Name', value: (r) => r.name),
          ],
          onTap: (_) {},
        ),
      ),
    ));
    await tester.pumpAndSettle();

    expect(find.text('No resources found'), findsOneWidget);
  });

  testWidgets('tablet empty list still shows "No resources found" '
      '(empty short-circuit runs before LayoutBuilder)', (tester) async {
    tester.view.physicalSize = const Size(900 * 2, 700 * 2);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(_harness(
      size: const Size(900, 700),
      child: Scaffold(
        body: ResourceTable<_Row>(
          items: const [],
          columns: [
            ResourceColumn(label: 'Name', value: (r) => r.name),
          ],
          onTap: (_) {},
        ),
      ),
    ));
    await tester.pumpAndSettle();

    expect(find.text('No resources found'), findsOneWidget);
    expect(find.byType(PaginatedDataTable2), findsNothing);
  });

  testWidgets('tablet table refreshes when items list reference changes via parent rebuild',
      (tester) async {
    tester.view.physicalSize = const Size(900 * 2, 700 * 2);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    var items = [_Row('alpha', 'Running')];

    Widget buildTable() => _harness(
          size: const Size(900, 700),
          child: Scaffold(
            body: ResourceTable<_Row>(
              items: items,
              columns: [
                ResourceColumn(label: 'Name', value: (r) => r.name),
                ResourceColumn(label: 'Status', value: (r) => r.status),
              ],
              onTap: (_) {},
            ),
          ),
        );

    await tester.pumpWidget(buildTable());
    await tester.pumpAndSettle();
    expect(find.text('alpha'), findsOneWidget);

    // New list reference → identical() guard fires → source.update() runs.
    items = [_Row('beta', 'Pending')];
    await tester.pumpWidget(buildTable());
    await tester.pumpAndSettle();

    expect(find.text('beta'), findsOneWidget);
    expect(find.text('alpha'), findsNothing);
  });
}
