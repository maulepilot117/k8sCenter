// ConfigMap list + detail. Both `data` (UTF-8) and `binaryData` (base64)
// are surfaced — pre-fix, binary keys silently dropped from the count
// and detail. Large values are truncated in the inline tile with a
// "Show full value" affordance to keep the SelectableText layout pass
// off the UI thread for Helm-rendered configmaps that embed multi-MB
// YAML / dashboard JSON.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_list_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

const int _inlineValueCap = 8 * 1024;

class _ConfigMapRow {
  _ConfigMapRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  Map<String, String> get data {
    final d = raw['data'] as Map<String, dynamic>? ?? const {};
    return d.map((k, v) => MapEntry(k, '$v'));
  }

  /// Binary keys (base64-encoded). Surfaced separately so binary entries
  /// don't get utf8-decoded into garbled text.
  Map<String, String> get binaryData {
    final d = raw['binaryData'] as Map<String, dynamic>? ?? const {};
    return d.map((k, v) => MapEntry(k, '$v'));
  }

  int get keyCount => data.length + binaryData.length;
}

class ConfigMapListScreen extends ConsumerWidget {
  const ConfigMapListScreen({super.key, this.namespace});
  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'ConfigMaps' : 'ConfigMaps · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'configmaps',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_ConfigMapRow.new).toList();
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
              kindDetailPath(
                clusterId: clusterId,
                kind: 'configmaps',
                namespace: r.meta.namespace,
                name: r.meta.name,
              ),
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
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: 'configmaps',
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
                title: 'DATA (${cm.data.length} text keys, '
                    '${cm.binaryData.length} binary)',
                child: cm.keyCount == 0
                    ? Text(
                        'No data',
                        style: TextStyle(color: colors.textMuted),
                      )
                    : Column(
                        children: [
                          for (final entry in cm.data.entries)
                            _DataKeyTile(
                              name: entry.key,
                              value: entry.value,
                              binary: false,
                            ),
                          for (final entry in cm.binaryData.entries)
                            _DataKeyTile(
                              name: entry.key,
                              value: entry.value,
                              binary: true,
                            ),
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
  const _DataKeyTile({
    required this.name,
    required this.value,
    required this.binary,
  });

  final String name;
  final String value;
  final bool binary;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final tooLarge = value.length > _inlineValueCap;
    final preview = tooLarge ? value.substring(0, _inlineValueCap) : value;
    return ExpansionTile(
      tilePadding: EdgeInsets.zero,
      title: Text(
        name,
        style: TextStyle(color: colors.textPrimary, fontSize: 13),
      ),
      subtitle: Text(
        binary
            ? '${value.length} chars (binary, base64)'
            : '${value.length} chars',
        style: TextStyle(color: colors.textMuted, fontSize: 11),
      ),
      children: [
        Container(
          width: double.infinity,
          padding: const EdgeInsets.all(8),
          color: colors.bgElevated,
          child: SelectableText(
            preview,
            style: const TextStyle(fontFamily: 'monospace', fontSize: 11),
          ),
        ),
        if (tooLarge)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  'Truncated to first ${_inlineValueCap ~/ 1024} KB '
                  '(${value.length} total)',
                  style: TextStyle(color: colors.textMuted, fontSize: 11),
                ),
                TextButton(
                  onPressed: () => _showFullValue(context, name, value),
                  child: const Text('Show full value'),
                ),
              ],
            ),
          ),
      ],
    );
  }

  void _showFullValue(BuildContext context, String key, String full) {
    Navigator.of(context).push<void>(
      MaterialPageRoute(
        builder: (_) => Scaffold(
          appBar: AppBar(title: Text(key)),
          body: SingleChildScrollView(
            padding: const EdgeInsets.all(16),
            child: SelectableText(
              full,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
            ),
          ),
        ),
      ),
    );
  }
}
