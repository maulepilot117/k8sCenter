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

    final label = clustersAsync.maybeWhen(
      data: (list) => list
          .firstWhere(
            (c) => c.id == activeId,
            orElse: () => localCluster,
          )
          .label,
      orElse: () => activeId,
    );

    return InkWell(
      key: const ValueKey('cluster-pill'),
      borderRadius: BorderRadius.circular(18),
      onTap: () => ClusterPickerSheet.show(context),
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
    );
  }
}
