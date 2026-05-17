// Secret list + detail. Values masked by default. Tapping Reveal calls
// the per-key audited reveal endpoint (`/v1/resources/secrets/:ns/:name/
// reveal/:key`) so each disclosure is logged server-side, matching the
// web frontend's audit model. Plaintext is sanitized for BiDi/control
// characters before render. Copy-to-clipboard requires explicit confirm
// and the clipboard is wiped after 30 seconds.
//
// The detail GET still returns the masked Secret (the YAML tab redacts
// `data`/`stringData` for kind == 'Secret' — see resource_detail_scaffold.dart).

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/resource_repository.dart';
import '../../api/yaml_apply_controller.dart';
import '../../cluster/cluster_provider.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/resource_actions_button.dart';
import '../../widgets/resource_detail_scaffold.dart';
import '../../widgets/resource_list_scaffold.dart';
import '../../widgets/resource_table.dart';
import '../../widgets/secure_screen_mixin.dart';
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
    final clusterId = ref.watch(activeClusterProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(namespace == null ? 'Secrets' : 'Secrets · $namespace'),
      ),
      body: ResourceListScaffold(
        providerKey: ResourceListKey(
          clusterId: clusterId,
          kind: 'secrets',
          namespace: namespace,
        ),
        builder: (context, result) {
          final rows = result.items.map(_SecretRow.new).toList();
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
              kindDetailPath(
                clusterId: clusterId,
                kind: 'secrets',
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

class _SecretDetailScreenState extends ConsumerState<SecretDetailScreen>
    with SecureScreenMixin<SecretDetailScreen> {
  /// Per-key revealed value cache. State is widget-scoped — back-button
  /// out of the screen disposes State, so re-entering the detail starts
  /// fresh with everything masked again.
  final Map<String, String> _revealed = {};
  final Set<String> _revealing = {};

  Future<void> _revealKey(String key) async {
    setState(() => _revealing.add(key));
    try {
      final value =
          await ref.read(resourceRepositoryProvider).revealSecretKey(
                namespace: widget.namespace,
                name: widget.name,
                key: key,
              );
      if (!mounted) return;
      setState(() {
        _revealed[key] = value;
        _revealing.remove(key);
      });
      _syncSensitivity();
    } on ApiError catch (e) {
      if (!mounted) return;
      setState(() => _revealing.remove(key));
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Reveal failed: ${e.message}')),
      );
    }
  }

  void _concealKey(String key) {
    setState(() => _revealed.remove(key));
    _syncSensitivity();
  }

  /// Flips FLAG_SECURE / iOS blur cover from `_revealed.isNotEmpty`. Stays
  /// armed while any key is revealed; clears once the last one is hidden.
  /// Mixin guards idempotency and serializes platform-channel calls — call
  /// site stays fire-and-forget so widget rebuilds don't stall on the
  /// FLAG_SECURE roundtrip.
  void _syncSensitivity() => unawaited(setSensitive(_revealed.isNotEmpty));

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final getKey = ResourceGetKey(
      clusterId: clusterId,
      kind: 'secrets',
      namespace: widget.namespace,
      name: widget.name,
    );
    final get = ref.watch(resourceGetProvider(getKey));
    return get.when(
      loading: () => const Scaffold(body: LoadingState()),
      error: (e, _) => Scaffold(
        appBar: AppBar(title: Text(widget.name)),
        body: ErrorStateView(
          message: e.toString(),
          onRetry: () => ref.invalidate(resourceGetProvider(getKey)),
        ),
      ),
      data: (raw) {
        final s = _SecretRow(raw);
        final colors = Theme.of(context).extension<KubeColors>()!;
        return ResourceDetailScaffold.secret(
          name: s.meta.name,
          namespace: s.meta.namespace,
          uid: s.meta.uid,
          statusLabel: s.type,
          statusColor: colors.warning,
          resource: raw,
          trailingAction: ResourceActionsButton(
            kind: 'secrets',
            namespace: s.meta.namespace,
            name: s.meta.name,
            resource: raw,
          ),
          editableYaml: true,
          applyKey: YamlApplyKey(
            clusterId: clusterId,
            kind: 'secrets',
            namespace: s.meta.namespace,
            name: s.meta.name,
          ),
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
                              keyName: entry.key,
                              revealed: _revealed[entry.key],
                              loading: _revealing.contains(entry.key),
                              onReveal: () => _revealKey(entry.key),
                              onHide: () => _concealKey(entry.key),
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
    required this.keyName,
    required this.revealed,
    required this.loading,
    required this.onReveal,
    required this.onHide,
  });

  final String keyName;
  final String? revealed;
  final bool loading;
  final VoidCallback onReveal;
  final VoidCallback onHide;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final isRevealed = revealed != null;
    return ListTile(
      contentPadding: EdgeInsets.zero,
      title: Text(
        keyName,
        style: TextStyle(color: colors.textPrimary, fontSize: 13),
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Text(
          isRevealed ? sanitizeSecretValue(revealed!) : '•' * 12,
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 12,
            fontFamily: 'monospace',
          ),
        ),
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (isRevealed)
            IconButton(
              key: ValueKey('secret-copy-$keyName'),
              icon: const Icon(Icons.copy, size: 18),
              tooltip: 'Copy value',
              onPressed: () => _confirmCopy(context, keyName, revealed!),
            ),
          if (loading)
            const SizedBox(
              width: 24,
              height: 24,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          else
            TextButton(
              key: ValueKey('secret-toggle-$keyName'),
              onPressed: isRevealed ? onHide : onReveal,
              child: Text(isRevealed ? 'Hide' : 'Reveal'),
            ),
        ],
      ),
    );
  }

  Future<void> _confirmCopy(
    BuildContext context,
    String key,
    String value,
  ) async {
    final messenger = ScaffoldMessenger.of(context);
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Copy secret to clipboard?'),
        content: Text(
          'The value for `$key` will be placed on the system clipboard '
          'and wiped after 30 seconds.\n\n'
          'On Android the clipboard may be readable by other apps. '
          'On iOS it may sync to other Apple devices via Universal Clipboard.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Copy'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await Clipboard.setData(ClipboardData(text: value));
    messenger.showSnackBar(
      const SnackBar(
        content: Text('Copied. Clipboard will clear in 30 seconds.'),
      ),
    );
    // Best-effort wipe — fails silently if app is backgrounded.
    Future.delayed(const Duration(seconds: 30), () async {
      final cur = await Clipboard.getData(Clipboard.kTextPlain);
      if (cur?.text == value) {
        await Clipboard.setData(const ClipboardData(text: ''));
      }
    });
  }
}

/// Escapes BiDi controls and other invisible/control characters in
/// revealed Secret values so a Trojan-Source-style payload (e.g., a
/// password followed by U+202E RIGHT-TO-LEFT OVERRIDE then a suffix
/// reversing visual order) can't trick an operator into copying a
/// different string than they see.
@visibleForTesting
String sanitizeSecretValue(String input) {
  final sb = StringBuffer();
  for (final code in input.runes) {
    if (_isUnsafe(code)) {
      sb.write('\\u${code.toRadixString(16).padLeft(4, '0')}');
    } else {
      sb.writeCharCode(code);
    }
  }
  return sb.toString();
}

bool _isUnsafe(int code) {
  // C0 controls except \t, \n, \r.
  if (code < 0x20 && code != 0x09 && code != 0x0A && code != 0x0D) return true;
  // DEL.
  if (code == 0x7F) return true;
  // C1 controls.
  if (code >= 0x80 && code <= 0x9F) return true;
  // BiDi formatting characters (Trojan-Source vector).
  const bidi = {
    0x202A, // LRE
    0x202B, // RLE
    0x202C, // PDF
    0x202D, // LRO
    0x202E, // RLO
    0x2066, // LRI
    0x2067, // RLI
    0x2068, // FSI
    0x2069, // PDI
  };
  if (bidi.contains(code)) return true;
  // Zero-width characters.
  const zeroWidth = {0x200B, 0x200C, 0x200D, 0xFEFF};
  if (zeroWidth.contains(code)) return true;
  return false;
}
