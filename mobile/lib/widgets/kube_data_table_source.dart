// Lazy DataTableSource backing the tablet ResourceTable layout. The
// previous `DataTable2(rows: [for ...])` path eagerly materialized
// every row up-front, so a 500-pod cluster paid 500 DataRow builds on
// the first frame — the dominant cost long before any of those rows
// scrolled into view. PaginatedDataTable2 calls `getRow(index)` only
// for the indices it needs to render, so the cost is bounded by
// `rowsPerPage` regardless of the underlying list size.

import 'package:data_table_2/data_table_2.dart';
import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';
import 'resource_table.dart';

class KubeDataTableSource<T> extends DataTableSource {
  KubeDataTableSource({
    required List<T> items,
    required List<ResourceColumn<T>> columns,
    required ValueChanged<T> onTap,
    required this.context,
  })  : _items = items,
        _columns = columns,
        _onTap = onTap;

  List<T> _items;
  List<ResourceColumn<T>> _columns;
  ValueChanged<T> _onTap;

  /// State.context of the hosting widget. State.context is stable for
  /// the lifetime of the State, so a single capture in initState gives
  /// getRow a context that always reflects the current theme.
  final BuildContext context;

  /// Test-only counter that increments on every non-null `getRow`
  /// return. Widget tests pump a PaginatedDataTable2 over a synthetic
  /// 6000-row source and assert this stays bounded by the visible page,
  /// proving virtualization actually kicks in.
  @visibleForTesting
  int rowCallCount = 0;

  int get itemCount => _items.length;

  /// Swap the backing data + columns + tap callback and notify the
  /// paginator to rebuild. Called by `_TabletTable.didUpdateWidget`
  /// when the parent rebuilds with new props.
  void update({
    required List<T> items,
    required List<ResourceColumn<T>> columns,
    required ValueChanged<T> onTap,
  }) {
    _items = items;
    _columns = columns;
    _onTap = onTap;
    rowCallCount = 0;
    notifyListeners();
  }

  @override
  DataRow? getRow(int index) {
    if (index < 0 || index >= _items.length) return null;
    rowCallCount++;
    final item = _items[index];
    final colors = Theme.of(context).extension<KubeColors>()!;
    return DataRow2(
      onTap: () => _onTap(item),
      cells: [
        for (final col in _columns)
          DataCell(
            Text(
              col.value(item),
              style: TextStyle(
                color: col.color?.call(context, item) ?? colors.textPrimary,
              ),
            ),
          ),
      ],
    );
  }

  @override
  int get rowCount => _items.length;

  @override
  bool get isRowCountApproximate => false;

  @override
  int get selectedRowCount => 0;
}
