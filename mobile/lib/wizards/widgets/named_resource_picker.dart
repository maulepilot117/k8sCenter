// Named-resource picker. Fetches a list of resources of a given kind
// (and optional namespace) via [resourceListProvider] and presents
// them as a tap-to-pick dropdown. Used by HPA's `scaleTargetRef.name`
// (deployments/statefulsets/replicasets) and RoleBinding's
// `roleRef.name` (roles/clusterroles).
//
// Cluster pinning: the wizard owns the pinned cluster id and threads
// it in via [clusterId]. We don't read `activeClusterProvider` here;
// `resourceListProvider` does (so cluster switches force a refetch),
// but the wizard's own cluster-pin discipline ensures the picker's
// fetch and the eventual apply target the same cluster.
//
// Empty state: "No <kind> in <namespace>" — the operator can switch
// namespace via their wizard's Namespace input, which causes the
// FutureProvider's family key to change and re-fetches.
//
// Loading/error states: thin progress indicator while loading; an
// inline error message with no retry button (the operator can change
// the namespace to retry implicitly, and aggressive auto-retry would
// hammer a struggling backend).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/resource_repository.dart';
import '../../theme/kube_theme_builder.dart';

class NamedResourcePicker extends ConsumerWidget {
  const NamedResourcePicker({
    super.key,
    required this.clusterId,
    required this.kind,
    required this.namespace,
    required this.selected,
    required this.onChanged,
    this.label,
    this.hint,
    this.errorMessage,
  });

  /// Pinned cluster (must match the wizard's pinned cluster). Threaded
  /// into [ResourceListKey] so the cache slot is correct.
  final String clusterId;

  /// Lowercase plural kind, e.g. `deployments`, `roles`, `clusterroles`.
  final String kind;

  /// Namespace to scope the list. Pass null for cluster-scoped kinds
  /// (clusterroles, storageclasses).
  final String? namespace;

  final String selected;
  final ValueChanged<String> onChanged;

  final String? label;
  final String? hint;
  final String? errorMessage;

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
          padding: EdgeInsets.symmetric(vertical: 12),
          child: LinearProgressIndicator(minHeight: 2),
        ),
      ),
      error: (e, _) => _frame(
        colors,
        Text(
          'Failed to load $kind: $e',
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
        final sorted = names.toList()..sort();

        if (sorted.isEmpty) {
          return _frame(
            colors,
            Text(
              namespace == null || namespace!.isEmpty
                  ? 'No $kind on this cluster'
                  : 'No $kind in $namespace',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          );
        }

        // If the currently-selected name is not in the list (e.g. the
        // operator pre-typed a name in a different namespace then
        // switched), add it so the dropdown can render the value.
        final values = {...sorted};
        if (selected.isNotEmpty) values.add(selected);
        final items = values.toList()..sort();

        return DropdownButtonFormField<String>(
          initialValue: selected.isEmpty ? null : selected,
          isExpanded: true,
          decoration: InputDecoration(
            labelText: label,
            hintText: hint,
            border: const OutlineInputBorder(),
            errorText: errorMessage,
          ),
          items: [
            for (final v in items)
              DropdownMenuItem(value: v, child: Text(v)),
          ],
          onChanged: (v) {
            if (v == null) return;
            onChanged(v);
          },
        );
      },
    );
  }

  Widget _frame(KubeColors colors, Widget child) {
    return Container(
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
            const SizedBox(height: 4),
          ],
          child,
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
