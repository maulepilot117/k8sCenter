// Fallback detail for any kind without a specialized screen. Renders only
// the YAML and Events tabs from `ResourceDetailScaffold` — no kind-specific
// overview content.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/resource_actions.dart';
import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_actions_button.dart';
import '../../widgets/resource_detail_scaffold.dart';
import 'k8s_helpers.dart';

class GenericDetailScreen extends ConsumerWidget {
  const GenericDetailScreen({
    super.key,
    required this.kind,
    required this.namespace,
    required this.name,
  });

  final String kind;
  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: kind,
      namespace: namespace,
      name: name,
    );
    final get = ref.watch(resourceGetProvider(getKey));
    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(name)),
        body: ErrorStateView(
          message: e.toString(),
          onRetry: () => ref.invalidate(resourceGetProvider(getKey)),
        ),
      ),
      data: (raw) {
        final meta = K8sMeta.from(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        // Generic detail picks up actions for any kind in `actionsByKind`
        // — Jobs/CronJobs go through this screen rather than a specialized
        // one, so this is where suspend/trigger become reachable for them.
        final hasActions =
            (actionsByKind[kind] ?? const []).isNotEmpty &&
                meta.namespace.isNotEmpty;
        return ResourceDetailScaffold(
          kindLabel: kind,
          name: meta.name,
          namespace: meta.namespace.isEmpty ? null : meta.namespace,
          uid: meta.uid,
          icon: Icons.inventory_2_outlined,
          resource: raw,
          isSensitive: kind.toLowerCase() == 'secret' ||
              kind.toLowerCase() == 'secrets',
          trailingAction: hasActions
              ? ResourceActionsButton(
                  kind: kind,
                  namespace: meta.namespace,
                  name: meta.name,
                  resource: raw,
                )
              : null,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'METADATA',
                child: Column(
                  children: [
                    DetailRow(label: 'Kind', value: kind),
                    DetailRow(
                      label: 'Created',
                      value: meta.creationTimestamp.isEmpty
                          ? '—'
                          : '${meta.creationTimestamp} (${formatAge(meta.creationTimestamp)})',
                    ),
                    if (meta.labels.isNotEmpty)
                      DetailRow(
                        label: 'Labels',
                        value: joinMap(meta.labels, maxEntries: 10),
                      ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(
                  'See the YAML tab for the full resource.',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}
