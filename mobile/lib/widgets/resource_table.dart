// Generic resource list adapter. Phone renders a card list; tablet
// (>= 768px) renders a DataTable. Column config is per-kind so the
// shared widget handles layout and the kind-specific screens specify
// what to show.

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';

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
        if (constraints.maxWidth >= 768) {
          return _buildTabletTable(context);
        }
        return _buildPhoneList(context);
      },
    );
  }

  Widget _buildPhoneList(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
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
          trailing: Icon(Icons.chevron_right, color: colors.textMuted),
          onTap: () => onTap(item),
        );
      },
    );
  }

  Widget _buildTabletTable(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return SingleChildScrollView(
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: DataTable(
          columns: [
            for (final col in columns)
              DataColumn(
                label: Text(
                  col.label,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
          ],
          rows: [
            for (final item in items)
              DataRow(
                onSelectChanged: (_) => onTap(item),
                cells: [
                  for (final col in columns)
                    DataCell(
                      Text(
                        col.value(item),
                        style: TextStyle(
                          color: col.color?.call(context, item) ??
                              colors.textPrimary,
                        ),
                      ),
                    ),
                ],
              ),
          ],
        ),
      ),
    );
  }
}
