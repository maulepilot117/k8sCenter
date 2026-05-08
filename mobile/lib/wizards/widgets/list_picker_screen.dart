// Generic list picker used as a step body inside a wizard. Lists
// resources of a kind (and optional namespace) and lets the operator
// tap one to select it. Mirrors M2's `RollbackPickerScreen` widget
// shape, generalized so wizards (RestoreSnapshot, etc.) can embed it
// inside their Configure step.
//
// Selection: the picker is *controlled* — the wizard owns the
// `selected` value and `onChanged` callback, identical to how
// `named_resource_picker.dart` is wired. This keeps the wizard's
// controller as the single source of truth and avoids a
// stateful-picker / wizard-form drift.
//
// Cluster pinning: identical pattern to `named_resource_picker.dart`.
// The wizard threads its pinned cluster id; the resource-list cache
// keys on it.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/resource_repository.dart';
import '../../theme/kube_theme_builder.dart';

/// Subtitle builder receives the raw resource map and returns an
/// optional secondary line. Lets callers surface size, age, etc.
typedef ListPickerSubtitleBuilder = String? Function(
    Map<String, dynamic> item);

class ListPickerScreen extends ConsumerWidget {
  const ListPickerScreen({
    super.key,
    required this.clusterId,
    required this.kind,
    required this.namespace,
    required this.selectedName,
    required this.onChanged,
    this.subtitleBuilder,
    this.emptyTitle,
    this.emptyMessage,
    this.errorMessage,
    this.maxHeight = 320,
  });

  final String clusterId;

  /// Lowercase plural kind (e.g. `volumesnapshots`, `backups`).
  final String kind;

  /// Namespace to scope the list. Pass null for cluster-scoped kinds.
  final String? namespace;

  /// Name of the currently-selected item. Empty string when nothing
  /// is selected yet.
  final String selectedName;

  final ValueChanged<String> onChanged;

  final ListPickerSubtitleBuilder? subtitleBuilder;

  final String? emptyTitle;
  final String? emptyMessage;
  final String? errorMessage;

  /// Cap the picker's height so the wizard's Next button stays
  /// reachable on small screens. The list inside scrolls.
  final double maxHeight;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final key = ResourceListKey(
      clusterId: clusterId,
      kind: kind,
      namespace: namespace,
    );
    final async = ref.watch(resourceListProvider(key));

    return async.when(
      loading: () => _frame(
        colors,
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 24),
          child: Center(child: CircularProgressIndicator()),
        ),
      ),
      error: (e, _) => _frame(
        colors,
        Padding(
          padding: const EdgeInsets.all(16),
          child: Text(
            'Failed to load $kind: $e',
            style: TextStyle(color: colors.error, fontSize: 12),
          ),
        ),
      ),
      data: (list) {
        if (list.items.isEmpty) {
          return _frame(
            colors,
            Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    emptyTitle ?? 'No $kind found',
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  if (emptyMessage != null) ...[
                    const SizedBox(height: 4),
                    Text(
                      emptyMessage!,
                      style:
                          TextStyle(color: colors.textMuted, fontSize: 12),
                    ),
                  ],
                ],
              ),
            ),
          );
        }
        return _frame(
          colors,
          ConstrainedBox(
            constraints: BoxConstraints(maxHeight: maxHeight),
            child: ListView.separated(
              shrinkWrap: true,
              itemCount: list.items.length,
              separatorBuilder: (_, _) =>
                  Divider(color: colors.borderSubtle, height: 1),
              itemBuilder: (context, i) {
                final item = list.items[i];
                final meta =
                    item['metadata'] as Map<String, dynamic>? ?? const {};
                final name = meta['name'] as String? ?? '';
                final selected = name == selectedName;
                final subtitle = subtitleBuilder?.call(item);
                return ListTile(
                  dense: true,
                  selected: selected,
                  selectedTileColor: colors.accent.withValues(alpha: 0.10),
                  leading: Icon(
                    selected
                        ? Icons.radio_button_checked
                        : Icons.radio_button_unchecked,
                    color: selected ? colors.accent : colors.textMuted,
                    size: 20,
                  ),
                  title: Text(
                    name,
                    style: TextStyle(color: colors.textPrimary),
                  ),
                  subtitle: subtitle == null
                      ? null
                      : Text(
                          subtitle,
                          style: TextStyle(
                            color: colors.textMuted,
                            fontSize: 12,
                          ),
                        ),
                  onTap: () => onChanged(name),
                );
              },
            ),
          ),
        );
      },
    );
  }

  Widget _frame(KubeColors colors, Widget child) {
    return Container(
      decoration: BoxDecoration(
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          child,
          if (errorMessage != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 6, 12, 10),
              child: Text(
                errorMessage!,
                style: TextStyle(color: colors.error, fontSize: 12),
              ),
            ),
        ],
      ),
    );
  }
}
