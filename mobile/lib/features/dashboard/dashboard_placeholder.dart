// PR-1a placeholder. PR-1c replaces this with the real dashboard backed
// by /v1/cluster/dashboard-summary.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';

class DashboardPlaceholder extends StatelessWidget {
  const DashboardPlaceholder({super.key});

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      color: colors.bgBase,
      child: const EmptyState(
        icon: Icons.cloud_outlined,
        title: 'k8sCenter',
        message: 'Dashboard arrives in PR-1c.\n'
            'Authentication ships in PR-1b.',
      ),
    );
  }
}
