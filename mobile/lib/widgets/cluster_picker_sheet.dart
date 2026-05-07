// Bottom-sheet cluster picker. Lists registered clusters with a radio
// row each; tap selects + pops. Admin-gated "Add cluster" entry routes to
// /clusters/new (registered in PR-1c+ when the registration screen lands;
// for now the entry is rendered but tapping it surfaces a "coming soon"
// SnackBar so the gating is testable).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../auth/auth_repository.dart';
import '../auth/auth_state.dart';
import '../cluster/cluster.dart';
import '../cluster/cluster_provider.dart';
import '../cluster/cluster_repository.dart';
import '../theme/kube_theme_builder.dart';
import 'empty_states.dart';

class ClusterPickerSheet extends ConsumerWidget {
  const ClusterPickerSheet({super.key});

  static Future<void> show(BuildContext context) {
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (_) => const ClusterPickerSheet(),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final activeId = ref.watch(activeClusterProvider);
    final clustersAsync = ref.watch(clustersProvider);
    final authState = ref.watch(authRepositoryProvider);
    final colors = Theme.of(context).extension<KubeColors>()!;
    final isAdmin =
        authState is AuthAuthenticated && authState.user.isAdmin;

    return SafeArea(
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.sizeOf(context).height * 0.7,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Text(
                'Cluster',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            Flexible(
              child: clustersAsync.when(
                data: (list) => _ClusterList(
                  clusters: list,
                  activeId: activeId,
                  onSelected: (id) {
                    ref.read(activeClusterProvider.notifier).setCluster(id);
                    Navigator.of(context).pop();
                  },
                ),
                loading: () => const SizedBox(
                  height: 120,
                  child: LoadingState(),
                ),
                error: (e, _) => SizedBox(
                  height: 160,
                  child: ErrorStateView(message: e.toString()),
                ),
              ),
            ),
            if (isAdmin)
              ListTile(
                key: const ValueKey('cluster-picker-add'),
                leading: Icon(Icons.add, color: colors.accent),
                title: Text(
                  'Add cluster',
                  style: TextStyle(color: colors.accent),
                ),
                onTap: () {
                  Navigator.of(context).pop();
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(
                      content:
                          Text('Cluster registration ships in a follow-up PR.'),
                    ),
                  );
                },
              ),
          ],
        ),
      ),
    );
  }
}

class _ClusterList extends StatelessWidget {
  const _ClusterList({
    required this.clusters,
    required this.activeId,
    required this.onSelected,
  });

  final List<Cluster> clusters;
  final String activeId;
  final ValueChanged<String> onSelected;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return RadioGroup<String>(
      groupValue: activeId,
      onChanged: (id) {
        if (id == null) return;
        onSelected(id);
      },
      child: ListView.builder(
        shrinkWrap: true,
        itemCount: clusters.length,
        itemBuilder: (context, index) {
          final c = clusters[index];
          return RadioListTile<String>(
            key: ValueKey('cluster-radio-${c.id}'),
            value: c.id,
            title: Text(c.label),
            subtitle: c.k8sVersion != null
                ? Text(
                    'k8s ${c.k8sVersion}',
                    style: TextStyle(color: colors.textMuted, fontSize: 12),
                  )
                : null,
            secondary: Icon(
              c.isLocal ? Icons.home_outlined : Icons.cloud_outlined,
              color: colors.textSecondary,
            ),
            activeColor: colors.accent,
          );
        },
      ),
    );
  }
}
