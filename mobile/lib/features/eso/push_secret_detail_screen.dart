// PushSecret detail — read-only. Surfaces the source Secret, the
// store-refs fan-out, and ready/lastSync timestamps. There is no
// action button — the v1 backend exposes no write surface.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import 'eso_widgets.dart';

class PushSecretDetailScreen extends ConsumerWidget {
  const PushSecretDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final key = PushSecretDetailKey(
      clusterId: clusterId,
      namespace: namespace,
      name: name,
    );
    final async = ref.watch(pushSecretDetailProvider(key));

    return Scaffold(
      appBar: AppBar(
        title: Text(name),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: () => ref.invalidate(pushSecretDetailProvider(key)),
          ),
        ],
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => esoDetailErrorState(
          error: e,
          onRetry: () => ref.invalidate(pushSecretDetailProvider(key)),
        ),
        data: (ps) => _Body(ps: ps),
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.ps});

  final PushSecret ps;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      children: [
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: colors.bgSurface,
            borderRadius: BorderRadius.circular(8),
            border: Border.all(color: colors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              EsoStatusPill(status: ps.status),
              const SizedBox(height: 10),
              Text(
                ps.namespace,
                style: TextStyle(color: colors.textSecondary, fontSize: 12),
              ),
              Text(
                ps.name,
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: 12),
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: colors.bgSurface,
            borderRadius: BorderRadius.circular(8),
            border: Border.all(color: colors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Configuration',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 8),
              if (ps.sourceSecretName != null)
                EsoKvRow(label: 'Source secret', value: ps.sourceSecretName!),
              if (ps.refreshInterval != null)
                EsoKvRow(label: 'Refresh interval', value: ps.refreshInterval!),
              if (ps.lastSyncTime != null)
                EsoKvRow(label: 'Last sync', value: ps.lastSyncTime!),
            ],
          ),
        ),
        if (ps.storeRefs.isNotEmpty) ...[
          const SizedBox(height: 12),
          Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: colors.bgSurface,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(color: colors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Pushes to',
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 8),
                for (final ref in ps.storeRefs)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    child: Row(
                      children: [
                        Icon(
                          ref.kind == 'ClusterSecretStore'
                              ? Icons.public_outlined
                              : Icons.account_tree_outlined,
                          size: 14,
                          color: colors.accent,
                        ),
                        const SizedBox(width: 6),
                        Expanded(
                          child: Text(
                            '${ref.kind} / ${ref.name}',
                            style: TextStyle(color: colors.textPrimary),
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            ),
          ),
        ],
        if (ps.readyMessage != null && ps.readyMessage!.isNotEmpty) ...[
          const SizedBox(height: 12),
          Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: colors.bgSurface,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(color: colors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  ps.readyReason ?? 'Status detail',
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 6),
                Text(
                  ps.readyMessage!,
                  style: TextStyle(color: colors.textSecondary),
                ),
              ],
            ),
          ),
        ],
      ],
    );
  }
}
