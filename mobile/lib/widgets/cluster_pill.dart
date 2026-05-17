// App-bar widget showing the active cluster's name. Tap opens the
// cluster picker bottom sheet.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../cluster/cluster_provider.dart';
import '../cluster/cluster_repository.dart';
import '../theme/kube_theme_builder.dart';
import 'cluster_picker_sheet.dart';

class ClusterPill extends ConsumerWidget {
  const ClusterPill({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final activeId = ref.watch(activeClusterProvider);
    final clustersAsync = ref.watch(clustersProvider);
    final colors = Theme.of(context).extension<KubeColors>()!;

    // Resolve a human-friendly label. While the list is loading or
    // errored we render a placeholder rather than leaking the raw id
    // (which may be a UUID for remote clusters).
    final label = clustersAsync.maybeWhen(
      data: (result) {
        final match = result.clusters.where((c) => c.id == activeId).toList();
        if (match.isNotEmpty) return match.first.label;
        // Active id is no longer in the list (deleted upstream); show the
        // id so the operator notices the inconsistency rather than seeing
        // a confident-looking 'local' fallback.
        return activeId;
      },
      orElse: () => '…',
    );

    // The outer Semantics owns both the label AND the tap action. Without
    // an onTap here, excludeSemantics: true would hide the InkWell's tap
    // action from the accessibility tree — VoiceOver/TalkBack users would
    // hear "button" but double-tap would do nothing.
    return Semantics(
      button: true,
      label: 'Active cluster: $label. Tap to switch cluster.',
      excludeSemantics: true,
      onTap: () => ClusterPickerSheet.show(context, ref),
      child: InkWell(
      key: const ValueKey('cluster-pill'),
      borderRadius: BorderRadius.circular(18),
      onTap: () => ClusterPickerSheet.show(context, ref),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        margin: const EdgeInsets.symmetric(horizontal: 4),
        decoration: BoxDecoration(
          color: colors.bgElevated,
          borderRadius: BorderRadius.circular(18),
          border: Border.all(color: colors.borderPrimary),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.cloud_outlined, size: 16, color: colors.accent),
            const SizedBox(width: 6),
            Text(
              label,
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
            const SizedBox(width: 4),
            Icon(Icons.expand_more, size: 16, color: colors.textSecondary),
          ],
        ),
      ),
    ),
    );
  }
}
