// ConfigMap list + detail.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

class _ConfigMapRow {
  _ConfigMapRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  Map<String, String> get data {
    final d = raw['data'] as Map<String, dynamic>? ?? const {};
    return d.map((k, v) => MapEntry(k, '$v'));
  }

  int get keyCount => data.length;
}

class ConfigMapListScreen extends ConsumerWidget {
  const ConfigMapListScreen({super.key, this.namespace});
  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final list = ref.watch(
      resourceListProvider(
        ResourceListKey(kind: 'configmaps', namespace: namespace),
      ),
    );
    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'ConfigMaps' : 'ConfigMaps · $namespace'),
      ),
      body: list.when(
        loading: () => const LoadingState(),
        error: (e, _) => ErrorStateView(message: e.toString()),
        data: (resp) {
          final rows = resp.items.map(_ConfigMapRow.new).toList();
          return ResourceTable<_ConfigMapRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(label: 'Namespace', value: (r) => r.meta.namespace),
              ResourceColumn(label: 'Keys', value: (r) => '${r.keyCount}'),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              '/clusters/local/config/configmaps/${r.meta.namespace}/${r.meta.name}',
            ),
          );
        },
      ),
    );
  }
}

class ConfigMapDetailScreen extends ConsumerWidget {
  const ConfigMapDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final get = ref.watch(
      resourceGetProvider(
        ResourceGetKey(kind: 'configmaps', namespace: namespace, name: name),
      ),
    );
    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(name)),
        body: ErrorStateView(message: e.toString()),
      ),
      data: (raw) {
        final cm = _ConfigMapRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'ConfigMap',
          name: cm.meta.name,
          namespace: cm.meta.namespace,
          icon: Icons.description_outlined,
          statusLabel: '${cm.keyCount} keys',
          statusColor: colors.accent,
          resource: raw,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'METADATA',
                child: Column(
                  children: [
                    DetailRow(
                      label: 'Created',
                      value: cm.meta.creationTimestamp.isEmpty
                          ? '—'
                          : '${cm.meta.creationTimestamp} (${formatAge(cm.meta.creationTimestamp)})',
                    ),
                    DetailRow(
                      label: 'Labels',
                      value: joinMap(cm.meta.labels, maxEntries: 10),
                    ),
                  ],
                ),
              ),
              DetailSection(
                title: 'DATA (${cm.keyCount} keys)',
                child: cm.data.isEmpty
                    ? Text(
                        'No data',
                        style: TextStyle(color: colors.textMuted),
                      )
                    : Column(
                        children: [
                          for (final entry in cm.data.entries)
                            _DataKeyTile(name: entry.key, value: entry.value),
                        ],
                      ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _DataKeyTile extends StatelessWidget {
  const _DataKeyTile({required this.name, required this.value});

  final String name;
  final String value;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ExpansionTile(
      tilePadding: EdgeInsets.zero,
      title: Text(
        name,
        style: TextStyle(color: colors.textPrimary, fontSize: 13),
      ),
      subtitle: Text(
        '${value.length} chars',
        style: TextStyle(color: colors.textMuted, fontSize: 11),
      ),
      children: [
        Container(
          width: double.infinity,
          padding: const EdgeInsets.all(8),
          color: colors.bgElevated,
          child: SelectableText(
            value,
            style: const TextStyle(fontFamily: 'monospace', fontSize: 11),
          ),
        ),
      ],
    );
  }
}
