// SecretStores list — namespaced stores. Cluster-scoped stores live on
// a parallel screen (`cluster_stores_list_screen.dart`) rather than
// being interleaved here; the desktop UI splits them similarly so
// scope confusion stays uncommon.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/eso_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/refresh_guard.dart';
import 'eso_widgets.dart';

class StoresListScreen extends StatelessWidget {
  const StoresListScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('SecretStores')),
      body: EsoStatusGate(
        builder: (clusterId) => _ListBody(clusterId: clusterId),
      ),
    );
  }
}

class _ListBody extends ConsumerStatefulWidget {
  const _ListBody({required this.clusterId});

  final String clusterId;

  @override
  ConsumerState<_ListBody> createState() => _ListBodyState();
}

class _ListBodyState extends ConsumerState<_ListBody>
    with RefreshGuardMixin {
  StoresListKey get _key => StoresListKey(clusterId: widget.clusterId);

  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(storesListProvider(_key));
        try {
          await ref.read(storesListProvider(_key).future);
        } on Object {/* surfaces via .when */}
      });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(storesListProvider(_key));

    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load SecretStores',
          error: e,
          onRetry: _handleRefresh,
        ),
        data: (items) {
          if (items.isEmpty) {
            return ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: [
                SizedBox(
                  height: 280,
                  child: Center(
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 32),
                      child: Text(
                        'No SecretStores visible. Create one with the '
                        '"SecretStore" wizard to start syncing secrets.',
                        style: TextStyle(color: colors.textMuted),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                ),
              ],
            );
          }
          return ListView.builder(
            physics: const AlwaysScrollableScrollPhysics(),
            itemCount: items.length,
            itemBuilder: (context, i) => _StoreRow(
              store: items[i],
              onTap: () => context.push(
                '/clusters/${widget.clusterId}/eso/stores/'
                '${Uri.encodeComponent(items[i].namespace)}/'
                '${Uri.encodeComponent(items[i].name)}',
              ),
            ),
          );
        },
      ),
    );
  }
}

class _StoreRow extends StatelessWidget {
  const _StoreRow({required this.store, required this.onTap});

  final SecretStore store;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Semantics(
      label: '${store.name}, namespace ${store.namespace}, '
          'provider ${store.provider.isEmpty ? 'unknown' : store.provider}, '
          'status ${store.status.name}',
      button: true,
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      store.name,
                      style: TextStyle(
                        color: colors.textPrimary,
                        fontSize: 15,
                        fontWeight: FontWeight.w600,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  EsoStatusPill(status: store.status, dense: true),
                ],
              ),
              const SizedBox(height: 4),
              Row(
                children: [
                  Text(
                    store.namespace,
                    style: TextStyle(color: colors.textSecondary, fontSize: 12),
                  ),
                  const SizedBox(width: 8),
                  ProviderChip(provider: store.provider),
                ],
              ),
              if (store.readyMessage != null && store.readyMessage!.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(
                    store.readyMessage!,
                    style: TextStyle(color: colors.textMuted, fontSize: 11),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
