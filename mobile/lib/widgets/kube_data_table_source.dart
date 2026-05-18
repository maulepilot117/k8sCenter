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

  int get itemCount => _items.length;

  /// Swap the backing data + columns + tap callback and notify the
  /// paginator to rebuild.
  ///
  /// `columns` is expected to be stable across rebuilds (defined as
  /// compile-time const lists at the caller). It is included in the
  /// signature for API symmetry, not because it changes frequently.
  /// The hot field is `items`; `onTap` typically also changes per
  /// rebuild because consumers pass inline lambdas — see the
  /// `didUpdateWidget` identity check in `_TabletTableState`.
  void update({
    required List<T> items,
    required List<ResourceColumn<T>> columns,
    required ValueChanged<T> onTap,
  }) {
    _items = items;
    _columns = columns;
    _onTap = onTap;
    notifyListeners();
  }

  @override
  DataRow? getRow(int index) {
    if (index < 0 || index >= _items.length) return null;
    final item = _items[index];
    final onTap = _onTap;
    final kubeColors = Theme.of(context).extension<KubeColors>();
    assert(kubeColors != null,
        'KubeColors ThemeExtension missing — wrap your widget in buildKubeTheme()');
    final colors = kubeColors!;
    return DataRow2(
      onTap: () => onTap(item),
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
