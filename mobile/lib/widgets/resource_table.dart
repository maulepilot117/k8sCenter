// Generic resource list adapter. Phone renders a card list; tablet
// (>= 768px) renders a paginated DataTable. Column config is per-kind
// so the shared widget handles layout and the kind-specific screens
// specify what to show.

import 'package:data_table_2/data_table_2.dart';
import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';
import 'adaptive_scaffold.dart';
import 'kube_data_table_source.dart';

/// Median-density column width for k8s name + numeric status cells.
/// Wide tables (8+ columns, e.g. PVC) overflow and DataTable2 enables
/// horizontal scroll automatically.
const double _kColumnWidth = 96.0;

/// Page size for the tablet paginator. Tracked in PERFORMANCE.md;
/// changing this requires re-measuring against the frame-budget targets.
const int _kRowsPerPage = 50;

/// One column in the resource table.
class ResourceColumn<T> {
  const ResourceColumn({
    required this.label,
    required this.value,
    this.color,
  });

  /// Column header label.
  final String label;

  /// Extracts the cell value as a string for the given row.
  final String Function(T item) value;

  /// Optional color override (per-row, e.g., status pills). Returns null
  /// to use the default text color.
  final Color? Function(BuildContext context, T item)? color;
}

class ResourceTable<T> extends StatelessWidget {
  const ResourceTable({
    super.key,
    required this.items,
    required this.columns,
    required this.onTap,
    this.primaryColumnIndex = 0,
  });

  final List<T> items;
  final List<ResourceColumn<T>> columns;
  final ValueChanged<T> onTap;

  /// Index into [columns] used as the card title on the phone layout.
  /// Defaults to the first column (typically "Name").
  final int primaryColumnIndex;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) {
      return const Center(
        child: Padding(
          padding: EdgeInsets.all(24),
          child: Text('No resources found'),
        ),
      );
    }

    return LayoutBuilder(
      builder: (context, constraints) {
        if (constraints.maxWidth >= tabletBreakpoint) {
          return _TabletTable<T>(
            items: items,
            columns: columns,
            onTap: onTap,
          );
        }
        return _buildPhoneList(context);
      },
    );
  }

  Widget _buildPhoneList(BuildContext context) {
    final kubeColors = Theme.of(context).extension<KubeColors>();
    assert(kubeColors != null,
        'KubeColors ThemeExtension missing — wrap your widget in buildKubeTheme()');
    final colors = kubeColors!;
    return ListView.separated(
      itemCount: items.length,
      padding: const EdgeInsets.symmetric(vertical: 8),
      separatorBuilder: (_, _) => Divider(
        height: 1,
        color: colors.borderSubtle,
      ),
      itemBuilder: (context, index) {
        final item = items[index];
        final primary = columns[primaryColumnIndex].value(item);
        final secondaryColumns = [
          for (var i = 0; i < columns.length; i++)
            if (i != primaryColumnIndex) columns[i],
        ];
        return ListTile(
          key: ValueKey('resource-row-$index'),
          title: Text(
            primary,
            style: TextStyle(
              color: colors.textPrimary,
              fontWeight: FontWeight.w500,
            ),
          ),
          subtitle: secondaryColumns.isEmpty
              ? null
              : Text(
                  secondaryColumns
                      .map((c) => '${c.label}: ${c.value(item)}')
                      .join(' · '),
                  style: TextStyle(color: colors.textSecondary, fontSize: 12),
                  overflow: TextOverflow.ellipsis,
                ),
          trailing: ExcludeSemantics(
            child: Icon(Icons.chevron_right, color: colors.textMuted),
          ),
          onTap: () => onTap(item),
        );
      },
    );
  }
}

// Tablet layout. Owns a long-lived [KubeDataTableSource] so
// PaginatedDataTable2 can request rows lazily via `getRow(index)`
// instead of the previous eager `rows: [for (item in items) ...]`
// path. 96px per column hits a comfortable median for k8s names +
// numeric status cells; wide tables (8+ columns like PVC) overflow
// `minWidth` and DataTable2 enables horizontal scroll automatically.
class _TabletTable<T> extends StatefulWidget {
  const _TabletTable({
    required this.items,
    required this.columns,
    required this.onTap,
  });

  final List<T> items;
  final List<ResourceColumn<T>> columns;
  final ValueChanged<T> onTap;

  @override
  State<_TabletTable<T>> createState() => _TabletTableState<T>();
}

class _TabletTableState<T> extends State<_TabletTable<T>> {
  late final KubeDataTableSource<T> _source;
  final PaginatorController _paginator = PaginatorController();

  @override
  void initState() {
    super.initState();
    _source = KubeDataTableSource<T>(
      items: widget.items,
      columns: widget.columns,
      onTap: widget.onTap,
      context: context,
    );
  }

  @override
  void didUpdateWidget(_TabletTable<T> oldWidget) {
    super.didUpdateWidget(oldWidget);
    // Clamp page before updating source: if the new list is shorter than
    // the current page start index, reset to page 0 to avoid an empty
    // "201–250 of 30" view. Guard isAttached because the controller is
    // wired on the first build, not on construction.
    if (_paginator.isAttached &&
        widget.items.length <= _paginator.currentRowIndex) {
      _paginator.goToFirstPage();
    }
    // onTap is intentionally excluded: the source's _onTap will pick up
    // the latest callback on the next genuine data-driven update() call.
    // Consumers always rebuild items alongside onTap, so staleness window
    // is bounded by the next items change.
    if (!identical(oldWidget.items, widget.items) ||
        !identical(oldWidget.columns, widget.columns)) {
      _source.update(
        items: widget.items,
        columns: widget.columns,
        onTap: widget.onTap,
      );
    }
  }

  @override
  void dispose() {
    // PaginatedDataTable2 is a direct child of this State, so Flutter
    // disposes it (removing its listener on _source) BEFORE this
    // dispose() runs. Safe to dispose _source and _paginator here. If
    // _source is ever hoisted to a sibling or a Riverpod provider,
    // re-evaluate this ordering — a notifier disposed while still has
    // listeners throws in debug mode.
    _paginator.dispose();
    _source.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final minWidth = widget.columns.length * _kColumnWidth;
    return PaginatedDataTable2(
      key: PageStorageKey<String>('resource_table_${T.toString()}'),
      source: _source,
      controller: _paginator,
      columns: [
        for (final col in widget.columns)
          DataColumn2(
            label: Text(
              col.label,
              style: TextStyle(
                color: colors.textSecondary,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
      ],
      columnSpacing: 16,
      horizontalMargin: 12,
      minWidth: minWidth,
      rowsPerPage: _kRowsPerPage,
      showCheckboxColumn: false,
      wrapInCard: false,
      // PR-5i review: avoid 40-blank-row allocation + "1-50 of 10" footer on short lists.
      renderEmptyRowsInTheEnd: false,
      hidePaginator: widget.items.length <= _kRowsPerPage,
    );
  }
}
