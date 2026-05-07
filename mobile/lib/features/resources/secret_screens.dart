// Secret list + detail. Values masked by default; per-key Reveal toggle
// surfaces the base64-decoded payload locally. There is no backend
// reveal-audit hook in PR-1d — the existing `/v1/resources/secrets/...`
// GET endpoint already returns the full Secret payload (audit logging
// happens at the request level on the backend). A future PR can add an
// explicit `/secrets/:ns/:name/reveal/:key` endpoint that audits per
// reveal click; for now the existing audit log captures the GET.

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_table.dart';
import 'k8s_helpers.dart';

class _SecretRow {
  _SecretRow(this.raw) : meta = K8sMeta.from(raw);
  final Map<String, dynamic> raw;
  final K8sMeta meta;

  String get type => raw['type'] as String? ?? 'Opaque';

  Map<String, String> get encodedData {
    final d = raw['data'] as Map<String, dynamic>? ?? const {};
    return d.map((k, v) => MapEntry(k, '$v'));
  }

  int get keyCount => encodedData.length;
}

class SecretListScreen extends ConsumerWidget {
  const SecretListScreen({super.key, this.namespace});
  final String? namespace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final list = ref.watch(
      resourceListProvider(
        ResourceListKey(kind: 'secrets', namespace: namespace),
      ),
    );
    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'Secrets' : 'Secrets · $namespace'),
      ),
      body: list.when(
        loading: () => const LoadingState(),
        error: (e, _) => ErrorStateView(message: e.toString()),
        data: (resp) {
          final rows = resp.items.map(_SecretRow.new).toList();
          return ResourceTable<_SecretRow>(
            items: rows,
            columns: [
              ResourceColumn(label: 'Name', value: (r) => r.meta.name),
              ResourceColumn(label: 'Namespace', value: (r) => r.meta.namespace),
              ResourceColumn(label: 'Type', value: (r) => r.type),
              ResourceColumn(label: 'Keys', value: (r) => '${r.keyCount}'),
              ResourceColumn(
                label: 'Age',
                value: (r) => formatAge(r.meta.creationTimestamp),
              ),
            ],
            onTap: (r) => context.push(
              '/clusters/local/config/secrets/${r.meta.namespace}/${r.meta.name}',
            ),
          );
        },
      ),
    );
  }
}

class SecretDetailScreen extends ConsumerStatefulWidget {
  const SecretDetailScreen({
    super.key,
    required this.namespace,
    required this.name,
  });

  final String namespace;
  final String name;

  @override
  ConsumerState<SecretDetailScreen> createState() => _SecretDetailScreenState();
}

class _SecretDetailScreenState extends ConsumerState<SecretDetailScreen> {
  final Set<String> _revealed = {};

  @override
  Widget build(BuildContext context) {
    final get = ref.watch(
      resourceGetProvider(
        ResourceGetKey(
          kind: 'secrets',
          namespace: widget.namespace,
          name: widget.name,
        ),
      ),
    );
    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(widget.name)),
        body: ErrorStateView(message: e.toString()),
      ),
      data: (raw) {
        final s = _SecretRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold(
          kindLabel: 'Secret',
          name: s.meta.name,
          namespace: s.meta.namespace,
          icon: Icons.key_outlined,
          statusLabel: s.type,
          statusColor: colors.warning,
          resource: raw,
          overview: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DetailSection(
                title: 'METADATA',
                child: Column(
                  children: [
                    DetailRow(label: 'Type', value: s.type),
                    DetailRow(
                      label: 'Created',
                      value: s.meta.creationTimestamp.isEmpty
                          ? '—'
                          : '${s.meta.creationTimestamp} (${formatAge(s.meta.creationTimestamp)})',
                    ),
                  ],
                ),
              ),
              DetailSection(
                title: 'DATA (${s.keyCount} keys)',
                child: s.encodedData.isEmpty
                    ? Text(
                        'No data',
                        style: TextStyle(color: colors.textMuted),
                      )
                    : Column(
                        children: [
                          for (final entry in s.encodedData.entries)
                            _SecretKeyTile(
                              name: entry.key,
                              encoded: entry.value,
                              revealed: _revealed.contains(entry.key),
                              onToggle: () => setState(() {
                                if (_revealed.contains(entry.key)) {
                                  _revealed.remove(entry.key);
                                } else {
                                  _revealed.add(entry.key);
                                }
                              }),
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

class _SecretKeyTile extends StatelessWidget {
  const _SecretKeyTile({
    required this.name,
    required this.encoded,
    required this.revealed,
    required this.onToggle,
  });

  final String name;
  final String encoded;
  final bool revealed;
  final VoidCallback onToggle;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return ListTile(
      contentPadding: EdgeInsets.zero,
      title: Text(
        name,
        style: TextStyle(color: colors.textPrimary, fontSize: 13),
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: SelectableText(
          revealed ? _safeDecode(encoded) : '•' * 12,
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 12,
            fontFamily: 'monospace',
          ),
        ),
      ),
      trailing: TextButton(
        onPressed: onToggle,
        child: Text(revealed ? 'Hide' : 'Reveal'),
      ),
    );
  }

  String _safeDecode(String b64) {
    try {
      return utf8.decode(base64.decode(b64));
    } catch (_) {
      // Binary value — render raw base64 so the operator can copy it.
      return '(binary, base64) $b64';
    }
  }
}
