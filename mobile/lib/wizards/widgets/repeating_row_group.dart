// Generic repeating sub-form widget. Renders an ordered list of rows
// with per-row Remove buttons and a trailing "+ Add" affordance. The
// parent owns the canonical list of items; this widget is a layout
// helper.
//
// Why not the trailing-empty-sentinel pattern from `KeyValueTable`?
// KeyValueTable's rows are two flat strings — sentinels work because
// the parent can trivially detect "both fields empty" and strip them.
// The complex rows this widget hosts (env vars, container ports,
// volume claim templates, probe handlers) have many fields and no
// universal "is empty" rule. An explicit Add button is cheaper than
// teaching every wizard a custom isEmpty.
//
// Each row's `Key` is its index — rebuilding the parent list keeps
// per-row state (TextEditingControllers etc.) stable for the same
// position. Remove always removes by index.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

class RepeatingRowGroup<T> extends StatelessWidget {
  const RepeatingRowGroup({
    super.key,
    required this.items,
    required this.itemBuilder,
    required this.onAdd,
    required this.onRemove,
    this.addLabel = 'Add row',
    this.emptyMessage,
    this.errorMessage,
  });

  final List<T> items;

  /// Renders one row's body. The widget parent owns the row's data;
  /// callbacks fire when the operator edits.
  final Widget Function(BuildContext context, int index, T item) itemBuilder;

  /// Append a fresh row. Parent decides what the default value looks
  /// like — typically a constructor that produces an empty record.
  final VoidCallback onAdd;

  /// Remove the row at [index] from the parent's list.
  final void Function(int index) onRemove;

  /// Label rendered on the Add button.
  final String addLabel;

  /// Optional message rendered when [items] is empty (e.g., "No env
  /// vars defined"). Falls back to nothing if null.
  final String? emptyMessage;

  /// Inline error rendered under the group (e.g., from preview-time
  /// field-level validation).
  final String? errorMessage;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (items.isEmpty && emptyMessage != null) ...[
          Text(
            emptyMessage!,
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
          const SizedBox(height: 8),
        ],
        for (var i = 0; i < items.length; i++)
          Padding(
            key: ValueKey('repeating-row-$i'),
            padding: const EdgeInsets.only(bottom: 12),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: itemBuilder(context, i, items[i]),
                ),
                const SizedBox(width: 4),
                IconButton(
                  visualDensity: VisualDensity.compact,
                  tooltip: 'Remove row',
                  onPressed: () => onRemove(i),
                  icon: Icon(Icons.close, color: colors.textMuted, size: 18),
                ),
              ],
            ),
          ),
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            onPressed: onAdd,
            icon: const Icon(Icons.add, size: 18),
            label: Text(addLabel),
          ),
        ),
        if (errorMessage != null) ...[
          const SizedBox(height: 4),
          Text(
            errorMessage!,
            style: TextStyle(color: colors.error, fontSize: 12),
          ),
        ],
      ],
    );
  }
}
