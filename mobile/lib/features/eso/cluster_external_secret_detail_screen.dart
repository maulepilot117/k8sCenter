// ClusterExternalSecret detail — cluster-scoped fan-out. Renders the
// store reference, the resolved namespace strategy (selector vs static
// list), provisioned + failed namespace chips, and the generated child
// ES base name.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import 'eso_widgets.dart';

class ClusterExternalSecretDetailScreen extends ConsumerWidget {
  const ClusterExternalSecretDetailScreen({super.key, required this.name});

  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final key = ClusterExternalSecretDetailKey(
      clusterId: clusterId,
      name: name,
    );
    final async = ref.watch(clusterExternalSecretDetailProvider(key));

    return Scaffold(
      appBar: AppBar(
        title: Text(name),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: () =>
                ref.invalidate(clusterExternalSecretDetailProvider(key)),
          ),
        ],
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => esoDetailErrorState(
          error: e,
          onRetry: () =>
              ref.invalidate(clusterExternalSecretDetailProvider(key)),
        ),
        data: (ces) => _Body(ces: ces),
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.ces});

  final ClusterExternalSecret ces;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      children: [
        _HeaderCard(ces: ces, colors: colors),
        const SizedBox(height: 12),
        _AttributesCard(ces: ces, colors: colors),
        if (ces.readyMessage != null && ces.readyMessage!.isNotEmpty) ...[
          const SizedBox(height: 12),
          EsoReadyMessageCard(
            reason: ces.readyReason,
            message: ces.readyMessage!,
            colors: colors,
          ),
        ],
        const SizedBox(height: 12),
        _NamespacesCard(ces: ces, colors: colors),
      ],
    );
  }
}

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({required this.ces, required this.colors});

  final ClusterExternalSecret ces;
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
              EsoStatusPill(status: ces.status),
              const SizedBox(width: 8),
              Text(
                'Cluster scope',
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ],
          ),
          const SizedBox(height: 10),
          Text(
            ces.name,
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
  const _AttributesCard({required this.ces, required this.colors});

  final ClusterExternalSecret ces;
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
          EsoKvRow(label: 'Store', value: '${ces.storeRef.kind} / ${ces.storeRef.name}'),
          if (ces.targetSecretName != null)
            EsoKvRow(label: 'Target secret', value: ces.targetSecretName!),
          if (ces.refreshInterval != null)
            EsoKvRow(label: 'Refresh interval', value: ces.refreshInterval!),
          if (ces.externalSecretBaseName != null)
            EsoKvRow(label: 'Child base name', value: ces.externalSecretBaseName!),
        ],
      ),
    );
  }
}

class _NamespacesCard extends StatelessWidget {
  const _NamespacesCard({required this.ces, required this.colors});

  final ClusterExternalSecret ces;
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
            'Namespaces',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          ChipStrip(label: 'Selectors', items: ces.namespaceSelectors),
          ChipStrip(label: 'Static list', items: ces.namespaces),
          ChipStrip(
            label: 'Provisioned',
            items: ces.provisionedNamespaces,
            foreground: colors.success,
          ),
          ChipStrip(
            label: 'Failed',
            items: ces.failedNamespaces,
            foreground: colors.error,
          ),
          if (ces.namespaceSelectors.isEmpty &&
              ces.namespaces.isEmpty &&
              ces.provisionedNamespaces.isEmpty &&
              ces.failedNamespaces.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text(
                'No namespaces resolved yet — controller may not have run, '
                'or the selector matches nothing.',
                style: TextStyle(color: colors.textMuted, fontSize: 12),
              ),
            ),
        ],
      ),
    );
  }
}
