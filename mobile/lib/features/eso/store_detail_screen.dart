// SecretStore detail — namespaced + cluster variants. Both share a
// tab scaffold: Overview / Metrics. The cluster variant is a thin
// wrapper that swaps the providers + URL shape.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import 'eso_widgets.dart';
import 'store_metrics_panel.dart';

class StoreDetailScreen extends ConsumerWidget {
  const StoreDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final key = StoreDetailKey(
      clusterId: clusterId,
      namespace: namespace,
      name: name,
    );
    final async = ref.watch(storeDetailProvider(key));
    final metricsAsync = ref.watch(storeMetricsProvider(key));

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: Text(name),
          actions: [
            IconButton(
              tooltip: 'Refresh',
              icon: const Icon(Icons.refresh),
              onPressed: () {
                ref.invalidate(storeDetailProvider(key));
                ref.invalidate(storeMetricsProvider(key));
              },
            ),
          ],
          bottom: const TabBar(
            tabs: [
              Tab(text: 'Overview'),
              Tab(text: 'Metrics'),
            ],
          ),
        ),
        body: async.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (e, _) => ErrorStateView(
            message: e is ApiError ? e.message : e.toString(),
            onRetry: () => ref.invalidate(storeDetailProvider(key)),
          ),
          data: (store) => TabBarView(
            children: [
              _OverviewTab(store: store),
              SingleChildScrollView(
                padding: const EdgeInsets.all(16),
                child: StoreMetricsPanel(
                  metricsAsync: metricsAsync,
                  onRetry: () => ref.invalidate(storeMetricsProvider(key)),
                  storeLabel: '${store.namespace} / ${store.name}',
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class ClusterStoreDetailScreen extends ConsumerWidget {
  const ClusterStoreDetailScreen({super.key, required this.name});

  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final key = ClusterStoreDetailKey(clusterId: clusterId, name: name);
    final async = ref.watch(clusterStoreDetailProvider(key));
    final metricsAsync = ref.watch(clusterStoreMetricsProvider(key));

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: Text(name),
          actions: [
            IconButton(
              tooltip: 'Refresh',
              icon: const Icon(Icons.refresh),
              onPressed: () {
                ref.invalidate(clusterStoreDetailProvider(key));
                ref.invalidate(clusterStoreMetricsProvider(key));
              },
            ),
          ],
          bottom: const TabBar(
            tabs: [
              Tab(text: 'Overview'),
              Tab(text: 'Metrics'),
            ],
          ),
        ),
        body: async.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (e, _) => ErrorStateView(
            message: e is ApiError ? e.message : e.toString(),
            onRetry: () => ref.invalidate(clusterStoreDetailProvider(key)),
          ),
          data: (store) => TabBarView(
            children: [
              _OverviewTab(store: store),
              SingleChildScrollView(
                padding: const EdgeInsets.all(16),
                child: StoreMetricsPanel(
                  metricsAsync: metricsAsync,
                  onRetry: () =>
                      ref.invalidate(clusterStoreMetricsProvider(key)),
                  storeLabel: store.name,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _OverviewTab extends StatelessWidget {
  const _OverviewTab({required this.store});

  final SecretStore store;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      children: [
        _HeaderCard(store: store, colors: colors),
        const SizedBox(height: 12),
        _AttributesCard(store: store, colors: colors),
        if (store.readyMessage != null && store.readyMessage!.isNotEmpty) ...[
          const SizedBox(height: 12),
          _ReadyMessageCard(
            reason: store.readyReason,
            message: store.readyMessage!,
            colors: colors,
          ),
        ],
        if (store.providerSpec.isNotEmpty) ...[
          const SizedBox(height: 12),
          _ProviderSpecCard(spec: store.providerSpec, colors: colors),
        ],
      ],
    );
  }
}

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({required this.store, required this.colors});

  final SecretStore store;
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
          Row(
            children: [
              EsoStatusPill(status: store.status),
              const SizedBox(width: 8),
              ProviderChip(provider: store.provider),
              const SizedBox(width: 8),
              if (store.isCluster)
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                  decoration: BoxDecoration(
                    color: colors.bgElevated,
                    borderRadius: BorderRadius.circular(3),
                    border: Border.all(color: colors.accent),
                  ),
                  child: Text(
                    'Cluster',
                    style: TextStyle(
                      color: colors.accent,
                      fontSize: 10,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 10),
          if (store.namespace.isNotEmpty)
            Text(
              store.namespace,
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
            ),
          Text(
            store.name,
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
  const _AttributesCard({required this.store, required this.colors});

  final SecretStore store;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final hasThresholds = store.staleAfterMinutes != null ||
        store.alertOnRecovery != null ||
        store.alertOnLifecycle != null;
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
          EsoKvRow(label: 'Scope', value: store.scope.isEmpty ? '—' : store.scope),
          EsoKvRow(label: 'Ready', value: store.ready ? 'yes' : 'no'),
          EsoKvRow(label: 'Provider', value: store.provider.isEmpty ? '—' : store.provider),
          if (hasThresholds) ...[
            const Divider(height: 16),
            Text(
              'Annotation thresholds',
              style: TextStyle(
                color: colors.textMuted,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 4),
            if (store.staleAfterMinutes != null)
              EsoKvRow(
                label: 'Stale after',
                value: '${store.staleAfterMinutes}m',
              ),
            if (store.alertOnRecovery != null)
              EsoKvRow(
                label: 'Alert on recovery',
                value: '${store.alertOnRecovery}',
              ),
            if (store.alertOnLifecycle != null)
              EsoKvRow(
                label: 'Alert on lifecycle',
                value: '${store.alertOnLifecycle}',
              ),
          ],
        ],
      ),
    );
  }
}

class _ReadyMessageCard extends StatelessWidget {
  const _ReadyMessageCard({
    required this.reason,
    required this.message,
    required this.colors,
  });

  final String? reason;
  final String message;
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
            reason ?? 'Status detail',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 6),
          Text(message, style: TextStyle(color: colors.textSecondary)),
        ],
      ),
    );
  }
}

class _ProviderSpecCard extends StatelessWidget {
  const _ProviderSpecCard({required this.spec, required this.colors});

  final Map<String, dynamic> spec;
  final KubeColors colors;

  /// Renders a single key/value from the provider spec. Values that
  /// are nested maps render as `[map: N keys]`; lists as `[list: N
  /// items]`. Drilling into nested provider spec is a desktop feature —
  /// mobile shows the addressing info verbatim. Credential fields are
  /// already filtered by the backend's normalize layer.
  Widget _row(BuildContext ctx, String k, Object? v) {
    String value;
    if (v == null) {
      value = 'null';
    } else if (v is Map) {
      value = '{${v.length} keys}';
    } else if (v is List) {
      value = '[${v.length} items]';
    } else {
      value = v.toString();
    }
    return EsoKvRow(label: k, value: value);
  }

  @override
  Widget build(BuildContext context) {
    final keys = spec.keys.toList()..sort();
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
            'Provider spec',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          for (final k in keys) _row(context, k, spec[k]),
          const SizedBox(height: 4),
          Text(
            'Open k8sCenter on desktop to drill into nested provider config.',
            style: TextStyle(color: colors.textMuted, fontSize: 11),
          ),
        ],
      ),
    );
  }
}
