// Multi-namespace picker. Renders a horizontal-scrolling row of chips
// representing each namespace on the cluster, with the operator-
// selected ones highlighted. Used by Velero Backup/Schedule (included
// + excluded namespace lists).
//
// Cluster pinning: the wizard owns the pinned cluster id and threads
// it via [clusterId]. The fetch goes through [resourceListProvider],
// which keys its cache on the cluster id, so a mid-wizard cluster
// switch can't surface another cluster's namespace list under the
// pinned cache slot — same pattern as `named_resource_picker.dart`.
//
// Empty/error/loading states are inline so the wizard's Configure step
// doesn't need to special-case them.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/resource_repository.dart';
import '../../theme/kube_theme_builder.dart';

class MultiNamespacePicker extends ConsumerWidget {
  const MultiNamespacePicker({
    super.key,
    required this.clusterId,
    required this.selected,
    required this.onChanged,
    this.label,
    this.helperText,
    this.errorMessage,
    this.disabledNamespaces = const <String>{},
  });

  /// Pinned cluster id. Threaded into [ResourceListKey] so the cache
  /// slot is correct.
  final String clusterId;

  /// Currently-selected namespace names.
  final Set<String> selected;

  /// Called whenever the operator toggles a chip.
  final ValueChanged<Set<String>> onChanged;

  final String? label;
  final String? helperText;
  final String? errorMessage;

  /// Namespaces to render disabled (e.g., to prevent picking a name in
  /// both included + excluded lists at the same time).
  final Set<String> disabledNamespaces;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final key = ResourceListKey(
      clusterId: clusterId,
      kind: 'namespaces',
    );
    final async = ref.watch(resourceListProvider(key));

    return async.when(
      loading: () => _frame(
        colors,
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 12),
          child: LinearProgressIndicator(minHeight: 2),
        ),
      ),
      error: (e, _) => _frame(
        colors,
        Text(
          'Failed to load namespaces: $e',
          style: TextStyle(color: colors.error, fontSize: 12),
        ),
      ),
      data: (list) {
        final names = <String>{};
        for (final item in list.items) {
          final meta = item['metadata'];
          if (meta is Map) {
            final n = meta['name'];
            if (n is String && n.isNotEmpty) names.add(n);
          }
        }
        // Include any selected names that aren't in the live list (e.g.
        // operator picked one then it got deleted) so toggling them off
        // is still possible.
        names.addAll(selected);
        final sorted = names.toList()..sort();

        if (sorted.isEmpty) {
          return _frame(
            colors,
            Text(
              'No namespaces visible to your account',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          );
        }

        return _frame(
          colors,
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final name in sorted)
                FilterChip(
                  label: Text(name),
                  selected: selected.contains(name),
                  onSelected: disabledNamespaces.contains(name)
                      ? null
                      : (on) {
                          final next = {...selected};
                          if (on) {
                            next.add(name);
                          } else {
                            next.remove(name);
                          }
                          onChanged(next);
                        },
                ),
            ],
          ),
        );
      },
    );
  }

  Widget _frame(KubeColors colors, Widget child) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (label != null) ...[
            Text(
              label!,
              style: TextStyle(
                color: colors.textMuted,
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 6),
          ],
          child,
          if (helperText != null) ...[
            const SizedBox(height: 6),
            Text(
              helperText!,
              style: TextStyle(color: colors.textMuted, fontSize: 11),
            ),
          ],
          if (errorMessage != null) ...[
            const SizedBox(height: 6),
            Text(
              errorMessage!,
              style: TextStyle(color: colors.error, fontSize: 12),
            ),
          ],
        ],
      ),
    );
  }
}
