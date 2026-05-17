// ExternalSecret detail — fetches the live `driftStatus` (the list
// endpoint only emits `lastObservedDriftStatus`; this screen is the
// source of truth for drift). Renders a read-only attribute dump, the
// store reference (tappable → store detail), and the Force Sync button
// (PR-5e). Drift-Unknown surfaces with the backend's reason tooltip so
// operators understand WHY drift wasn't resolvable. The Force Sync
// button is disabled on non-local clusters (backend returns 501 for
// remote ESO writes); on success it triggers a snackbar and the drift
// chip refreshes via `externalSecretDetailProvider` invalidation.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import 'eso_widgets.dart';

class ExternalSecretDetailScreen extends ConsumerWidget {
  const ExternalSecretDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final key = ExternalSecretDetailKey(
      clusterId: clusterId,
      namespace: namespace,
      name: name,
    );
    final async = ref.watch(externalSecretDetailProvider(key));

    return Scaffold(
      appBar: AppBar(
        title: Text(name),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: () => ref.invalidate(externalSecretDetailProvider(key)),
          ),
        ],
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => esoDetailErrorState(
          error: e,
          onRetry: () => ref.invalidate(externalSecretDetailProvider(key)),
        ),
        data: (es) => _Body(clusterId: clusterId, es: es),
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.clusterId, required this.es});

  final String clusterId;
  final ExternalSecret es;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      children: [
        _HeaderCard(es: es, colors: colors),
        const SizedBox(height: 12),
        _AttributesCard(es: es, clusterId: clusterId, colors: colors),
        if (es.readyMessage != null && es.readyMessage!.isNotEmpty) ...[
          const SizedBox(height: 12),
          EsoReadyMessageCard(
            reason: es.readyReason,
            message: es.readyMessage!,
            colors: colors,
          ),
        ],
        if (es.staleAfterMinutes != null ||
            es.alertOnRecovery != null ||
            es.alertOnLifecycle != null) ...[
          const SizedBox(height: 12),
          _ThresholdsCard(es: es, colors: colors),
        ],
      ],
    );
  }
}

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({required this.es, required this.colors});

  final ExternalSecret es;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final drift = es.driftStatus;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              EsoStatusPill(status: es.status),
              const SizedBox(width: 8),
              if (drift != DriftStatus.notObserved)
                DriftPill(status: drift, reason: es.driftUnknownReason),
              const Spacer(),
              // Force Sync (PR-5e). Disabled on non-local clusters; the
              // tooltip on the disabled state explains why.
              ForceSyncButton(namespace: es.namespace, name: es.name),
            ],
          ),
          const SizedBox(height: 12),
          Text(
            es.namespace,
            style: TextStyle(color: colors.textSecondary, fontSize: 12),
          ),
          const SizedBox(height: 4),
          Text(
            es.name,
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 18,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
  }
}

class _AttributesCard extends StatelessWidget {
  const _AttributesCard({
    required this.es,
    required this.clusterId,
    required this.colors,
  });

  final ExternalSecret es;
  final String clusterId;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
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
          // Tappable store row — links to store detail in the matching
          // section. Distinguishes namespaced vs cluster store via the
          // ref kind, mirrors web's deep-link behaviour.
          _StoreRefLink(
            storeRef: es.storeRef,
            esNamespace: es.namespace,
            clusterId: clusterId,
            colors: colors,
          ),
          if (es.targetSecretName != null)
            EsoKvRow(
              label: 'Target secret',
              value: es.targetSecretName!,
            ),
          if (es.refreshInterval != null)
            EsoKvRow(
              label: 'Refresh interval',
              value: es.refreshInterval!,
            ),
          if (es.lastSyncTime != null)
            EsoKvRow(
              label: 'Last sync',
              value: es.lastSyncTime!,
            ),
          if (es.syncedResourceVersion != null)
            EsoKvRow(
              label: 'Synced RV',
              value: es.syncedResourceVersion!,
            ),
        ],
      ),
    );
  }
}

class _StoreRefLink extends StatelessWidget {
  const _StoreRefLink({
    required this.storeRef,
    required this.esNamespace,
    required this.clusterId,
    required this.colors,
  });

  final EsoStoreRef storeRef;
  final String esNamespace;
  final String clusterId;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final ref = storeRef;
    if (ref.name.isEmpty) {
      return EsoKvRow(label: 'Store', value: '(none)');
    }
    final isCluster = ref.kind == 'ClusterSecretStore';
    final path = isCluster
        ? '/clusters/$clusterId/eso/cluster-stores/'
            '${Uri.encodeComponent(ref.name)}'
        : '/clusters/$clusterId/eso/stores/'
            '${Uri.encodeComponent(esNamespace)}/'
            '${Uri.encodeComponent(ref.name)}';
    return Semantics(
      label: 'Open ${ref.kind} ${ref.name}',
      button: true,
      child: InkWell(
        onTap: () => context.push(path),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(
                width: 120,
                child: Text(
                  'Store',
                  style: TextStyle(
                    color: colors.textMuted,
                    fontSize: 12,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
              Expanded(
                child: Row(
                  children: [
                    ExcludeSemantics(
                      child: Icon(
                        isCluster ? Icons.public_outlined : Icons.account_tree_outlined,
                        size: 14,
                        color: colors.accent,
                      ),
                    ),
                    const SizedBox(width: 6),
                    Flexible(
                      child: Text(
                        '${ref.kind} / ${ref.name}',
                        style: TextStyle(
                          color: colors.accent,
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const SizedBox(width: 4),
                    ExcludeSemantics(
                      child: Icon(
                        Icons.open_in_new,
                        size: 12,
                        color: colors.accent,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ThresholdsCard extends StatelessWidget {
  const _ThresholdsCard({required this.es, required this.colors});

  final ExternalSecret es;
  final KubeColors colors;

  String _sourceLabel(EsoThresholdSource s) => switch (s) {
        EsoThresholdSource.packageDefault => 'package default',
        EsoThresholdSource.externalSecret => 'ExternalSecret',
        EsoThresholdSource.secretStore => 'SecretStore',
        EsoThresholdSource.clusterSecretStore => 'ClusterSecretStore',
        EsoThresholdSource.unknown => '—',
      };

  @override
  Widget build(BuildContext context) {
    return Container(
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
            'Resolved thresholds',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          if (es.staleAfterMinutes != null)
            EsoKvRow(
              label: 'Stale after',
              value:
                  '${es.staleAfterMinutes}m  ·  source: ${_sourceLabel(es.staleAfterMinutesSource)}',
            ),
          if (es.alertOnRecovery != null)
            EsoKvRow(
              label: 'Alert on recovery',
              value:
                  '${es.alertOnRecovery}  ·  source: ${_sourceLabel(es.alertOnRecoverySource)}',
            ),
          if (es.alertOnLifecycle != null)
            EsoKvRow(
              label: 'Alert on lifecycle',
              value:
                  '${es.alertOnLifecycle}  ·  source: ${_sourceLabel(es.alertOnLifecycleSource)}',
            ),
        ],
      ),
    );
  }
}
