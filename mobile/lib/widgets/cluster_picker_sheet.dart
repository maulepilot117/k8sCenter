// Bottom-sheet cluster picker. Lists registered clusters with a radio
// row each; tap selects + pops. Admin-gated "Add cluster" entry routes to
// /clusters/new (registered in PR-1c+ when the registration screen lands;
// for now the entry is rendered but tapping it surfaces a "coming soon"
// SnackBar so the gating is testable).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/api_error.dart';
import '../auth/auth_repository.dart';
import '../auth/auth_state.dart';
import '../cluster/cluster_provider.dart';
import '../cluster/cluster_repository.dart';
import '../theme/kube_theme_builder.dart';
import 'empty_states.dart';

class ClusterPickerSheet extends ConsumerWidget {
  const ClusterPickerSheet({super.key});

  /// Opens the picker. Forces a fresh `/v1/clusters` fetch so users
  /// always see the current state when selecting — stale lists let
  /// operators pick a cluster that was deleted upstream.
  static Future<void> show(BuildContext context, WidgetRef ref) {
    ref.invalidate(clustersProvider);
    return showModalBottomSheet<void>(
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
                data: (result) => _ClusterList(
                  result: result,
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
                  height: 200,
                  child: ErrorStateView(
                    message: e is ApiError ? e.message : 'Failed to load clusters',
                    onRetry: () => ref.invalidate(clustersProvider),
                  ),
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
                  // Capture messenger before pop — once the sheet closes
                  // its context is deactivated and ScaffoldMessenger.of
                  // looks up against a torn-down element. Capturing
                  // first sidesteps the use_build_context_synchronously
                  // class of bug.
                  final messenger = ScaffoldMessenger.of(context);
                  Navigator.of(context).pop();
                  messenger.showSnackBar(
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
    required this.result,
    required this.activeId,
    required this.onSelected,
  });

  final ClusterListResult result;
  final String activeId;
  final ValueChanged<String> onSelected;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (result.degraded)
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            margin: const EdgeInsets.symmetric(horizontal: 16),
            decoration: BoxDecoration(
              color: colors.warningDim,
              borderRadius: BorderRadius.circular(6),
            ),
            child: Row(
              children: [
                ExcludeSemantics(child: Icon(Icons.cloud_off, size: 16, color: colors.warning)),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    'Backend unreachable — showing local cluster only.',
                    style: TextStyle(color: colors.warning, fontSize: 12),
                  ),
                ),
              ],
            ),
          ),
        Flexible(
          child: RadioGroup<String>(
            groupValue: activeId,
            onChanged: (id) {
              if (id == null) return;
              onSelected(id);
            },
            child: ListView.builder(
              shrinkWrap: true,
              itemCount: result.clusters.length,
              itemBuilder: (context, index) {
                final c = result.clusters[index];
                return RadioListTile<String>(
                  key: ValueKey('cluster-radio-${c.id}'),
                  value: c.id,
                  // F#10 — surface "⚠ Insecure TLS" next to the cluster
                  // label whenever the admin opted into unverified TLS.
                  // Bearer tokens travel over the connection; the badge
                  // is intentionally noisy so a misconfigured production
                  // cluster gets noticed before it's used.
                  title: Row(
                    children: [
                      Flexible(child: Text(c.label)),
                      if (c.allowInsecureTLS && !c.isLocal) ...[
                        const SizedBox(width: 8),
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 6,
                            vertical: 2,
                          ),
                          decoration: BoxDecoration(
                            color: colors.warningDim,
                            borderRadius: BorderRadius.circular(4),
                          ),
                          child: Text(
                            '⚠ Insecure TLS',
                            style: TextStyle(
                              color: colors.warning,
                              fontSize: 10,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                  subtitle: c.k8sVersion != null
                      ? Text(
                          'k8s ${c.k8sVersion}',
                          style: TextStyle(
                            color: colors.textMuted,
                            fontSize: 12,
                          ),
                        )
                      : null,
                  secondary: ExcludeSemantics(
                    child: Icon(
                      c.isLocal ? Icons.home_outlined : Icons.cloud_outlined,
                      color: colors.textSecondary,
                    ),
                  ),
                  activeColor: colors.accent,
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}
