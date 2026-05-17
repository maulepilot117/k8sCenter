// KubeDataTableSource tests: lazy row materialization, edge indices,
// update + notify, and end-to-end virtualization via PaginatedDataTable2.

import 'package:data_table_2/data_table_2.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/kube_data_table_source.dart';
import 'package:kubecenter/widgets/resource_table.dart';

class _Row {
  const _Row(this.name, this.status);
  final String name;
  final String status;
}

Widget _harness({required Widget child, required Size size}) {
  return MaterialApp(
    theme: buildKubeTheme('nexus'),
    home: MediaQuery(
      data: MediaQueryData(size: size),
      child: SizedBox.fromSize(size: size, child: child),
    ),
  );
}

/// Pumps a no-op widget tree and returns a BuildContext the source can
/// look up Theme.of(context) against. The widget itself is a Builder
/// that exposes the inner element to the test via a callback.
Future<BuildContext> _pumpContext(WidgetTester tester) async {
  late BuildContext captured;
  await tester.pumpWidget(_harness(
    size: const Size(900, 700),
    child: Scaffold(
      body: Builder(
        builder: (context) {
          captured = context;
          return const SizedBox.shrink();
        },
      ),
    ),
  ));
  await tester.pumpAndSettle();
  return captured;
}

KubeDataTableSource<_Row> _source(BuildContext context, List<_Row> items, {
  ValueChanged<_Row>? onTap,
}) {
  return KubeDataTableSource<_Row>(
    items: items,
    columns: [
      ResourceColumn(label: 'Name', value: (r) => r.name),
      ResourceColumn(label: 'Status', value: (r) => r.status),
    ],
    onTap: onTap ?? (_) {},
    context: context,
  );
}

void main() {
  testWidgets('getRow(0) returns the first row from a 6000-item source',
      (tester) async {
    final context = await _pumpContext(tester);
    final items = List.generate(6000, (i) => _Row('name-$i', 'status-$i'));
    final source = _source(context, items);

    final row = source.getRow(0);
    expect(row, isNotNull);
    expect(row, isA<DataRow2>());
    final firstCell = (row!.cells.first.child as Text).data;
    expect(firstCell, 'name-0');
  });

  testWidgets('getRow(5999) returns the last row of a 6000-item source',
      (tester) async {
    final context = await _pumpContext(tester);
    final items = List.generate(6000, (i) => _Row('name-$i', 'status-$i'));
    final source = _source(context, items);

    final row = source.getRow(5999);
    expect(row, isNotNull);
    final firstCell = (row!.cells.first.child as Text).data;
    expect(firstCell, 'name-5999');
  });

  testWidgets('getRow returns null for out-of-range indices', (tester) async {
    final context = await _pumpContext(tester);
    final source = _source(context, const [_Row('only', 'Running')]);

    expect(source.getRow(-1), isNull);
    expect(source.getRow(1), isNull);
    expect(source.getRow(0), isNotNull);
  });

  testWidgets('rowCount mirrors items.length', (tester) async {
    final context = await _pumpContext(tester);
    final source = _source(context, [
      const _Row('a', 'Running'),
      const _Row('b', 'Pending'),
      const _Row('c', 'Failed'),
    ]);
    expect(source.rowCount, 3);
  });

  testWidgets('update() replaces items, resets rowCallCount, and notifies',
      (tester) async {
    final context = await _pumpContext(tester);
    final source = _source(context, [const _Row('a', 'Running')]);

    source.getRow(0);
    expect(source.rowCallCount, 1);

    var notified = 0;
    source.addListener(() => notified++);

    source.update(
      items: [const _Row('x', 'Pending'), const _Row('y', 'Failed')],
      columns: [
        ResourceColumn(label: 'Name', value: (r) => r.name),
        ResourceColumn(label: 'Status', value: (r) => r.status),
      ],
      onTap: (_) {},
    );

    expect(notified, 1);
    expect(source.rowCount, 2);
    expect(source.rowCallCount, 0);

    final row = source.getRow(0);
    expect((row!.cells.first.child as Text).data, 'x');
    expect(source.rowCallCount, 1);
  });

  testWidgets('row onTap callback fires with the underlying item',
      (tester) async {
    final context = await _pumpContext(tester);
    _Row? tapped;
    final source = _source(
      context,
      const [_Row('alpha', 'Running')],
      onTap: (r) => tapped = r,
    );

    final row = source.getRow(0) as DataRow2;
    row.onTap!.call();
    expect(tapped?.name, 'alpha');
  });

  testWidgets(
      'PaginatedDataTable2 only materializes rows for the visible page',
      (tester) async {
    tester.view.physicalSize = const Size(900 * 2, 700 * 2);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final items = List.generate(6000, (i) => _Row('name-$i', 'status-$i'));
    late KubeDataTableSource<_Row> source;

    await tester.pumpWidget(_harness(
      size: const Size(900, 700),
      child: Scaffold(
        body: Builder(
          builder: (context) {
            source = KubeDataTableSource<_Row>(
              items: items,
              columns: [
                ResourceColumn(label: 'Name', value: (r) => r.name),
                ResourceColumn(label: 'Status', value: (r) => r.status),
              ],
              onTap: (_) {},
              context: context,
            );
            return PaginatedDataTable2(
              source: source,
              columns: const [
                DataColumn2(label: Text('Name')),
                DataColumn2(label: Text('Status')),
              ],
              rowsPerPage: 50,
              showCheckboxColumn: false,
              wrapInCard: false,
            );
          },
        ),
      ),
    ));
    await tester.pumpAndSettle();

    // The visible page is 50 rows. PaginatedDataTable2 may request a
    // couple extra indices for measurement/empty-row padding, so 60 is
    // the safe bound that still proves virtualization (vs the 6000 a
    // non-lazy implementation would request).
    expect(source.rowCallCount, lessThan(60),
        reason: 'Source requested ${source.rowCallCount} rows '
            '(must stay bounded by the visible page, not the 6000-item list)');
  });
}
