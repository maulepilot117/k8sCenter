// Combined SecretStore + ClusterSecretStore picker. Used by the
// ExternalSecret wizard to select a `spec.secretStoreRef`. Surfaces
// both namespaced SecretStores (filtered to the wizard's active
// namespace) and cluster-scoped ClusterSecretStores in a single
// dropdown, distinguished by a leading icon. Mirrors `issuer_picker.dart`
// shape — the same defense pattern carries: cluster-pinned headers,
// namespace filtering at the picker layer, typed selection record.
//
// Backend endpoints:
//   GET /api/v1/externalsecrets/stores?namespace=<ns>  → namespaced stores
//                                                       (server filters by ns)
//   GET /api/v1/externalsecrets/clusterstores          → cluster stores
//
// The list-stores endpoint accepts a `?namespace=` query and the
// server filters server-side; we still pass the wizard's namespace and
// rely on the server filter. ClusterSecretStores are namespace-agnostic
// — every namespaced ExternalSecret can reference any cluster store.
//
// Cluster pinning: clusterId threaded through as X-Cluster-ID so a
// mid-flight switch of activeClusterProvider can't redirect the fetch.
// Mirrors `issuer_picker.dart` and PR-3c's NamedResourcePicker.

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/dio_client.dart';
import '../../theme/kube_theme_builder.dart';

/// One entry in the combined picker. Carries the name and the kind
/// ("SecretStore" or "ClusterSecretStore") so the wizard can fill both
/// `secretStoreRef.name` and `secretStoreRef.kind` from a single tap.
class StoreSelection {
  const StoreSelection({required this.name, required this.kind});

  /// SecretStore / ClusterSecretStore metadata.name.
  final String name;

  /// "SecretStore" or "ClusterSecretStore". Matches ESO's Kind values
  /// and the backend's ExternalSecretStoreRefInput.Kind expectation.
  final String kind;

  @override
  bool operator ==(Object other) =>
      other is StoreSelection && other.name == name && other.kind == kind;

  @override
  int get hashCode => Object.hash(name, kind);
}

/// One entry from the list endpoints. Provider is surfaced as a small
/// hint next to the name so operators don't have to remember which
/// store maps to which backend.
class StoreOption {
  const StoreOption({
    required this.name,
    required this.kind,
    required this.provider,
  });

  final String name;
  final String kind;
  final String provider;
}

/// Compound result from the two list endpoints.
class _StoreLists {
  const _StoreLists({required this.namespaced, required this.cluster});
  final List<StoreOption> namespaced;
  final List<StoreOption> cluster;
}

/// Family key — clusterId pins the cache slot, namespace scopes the
/// namespaced-store filter. Cluster-scoped stores are always included.
class StoreListKey {
  const StoreListKey({required this.clusterId, required this.namespace});

  final String clusterId;
  final String namespace;

  @override
  bool operator ==(Object other) =>
      other is StoreListKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace;

  @override
  int get hashCode => Object.hash(clusterId, namespace);
}

/// Fetches namespaced + cluster-scoped stores in parallel. Returns
/// empty lists when ESO isn't installed (the backend returns `data: []`
/// with 200 in that case).
final storeListProvider = FutureProvider.autoDispose
    .family<_StoreLists, StoreListKey>((ref, key) async {
  final dio = ref.watch(dioProvider);
  final headers = {'X-Cluster-ID': key.clusterId};

  Future<List<dynamic>> fetch(String path,
      {Map<String, dynamic>? query}) async {
    try {
      final res = await dio.get<Map<String, dynamic>>(
        path,
        queryParameters: query,
        options: Options(headers: headers),
      );
      final data = res.data?['data'];
      return data is List ? data : const [];
    } on DioException catch (e) {
      final err = e.error;
      throw err is ApiError ? err : ApiError.fromDio(e);
    }
  }

  final results = await Future.wait([
    fetch(
      '/api/v1/externalsecrets/stores',
      query: key.namespace.isEmpty ? null : {'namespace': key.namespace},
    ),
    fetch('/api/v1/externalsecrets/clusterstores'),
  ]);

  StoreOption? toOption(dynamic raw, String kind) {
    if (raw is! Map) return null;
    final name = raw['name'];
    if (name is! String || name.isEmpty) return null;
    final provider = raw['provider'];
    return StoreOption(
      name: name,
      kind: kind,
      provider: provider is String ? provider : '',
    );
  }

  final namespaced = <StoreOption>[];
  for (final item in results[0]) {
    final opt = toOption(item, 'SecretStore');
    if (opt != null) namespaced.add(opt);
  }
  final cluster = <StoreOption>[];
  for (final item in results[1]) {
    final opt = toOption(item, 'ClusterSecretStore');
    if (opt != null) cluster.add(opt);
  }
  namespaced.sort((a, b) => a.name.compareTo(b.name));
  cluster.sort((a, b) => a.name.compareTo(b.name));
  return _StoreLists(namespaced: namespaced, cluster: cluster);
});

class StorePicker extends ConsumerWidget {
  const StorePicker({
    super.key,
    required this.clusterId,
    required this.namespace,
    required this.selected,
    required this.onChanged,
    this.label,
    this.errorMessage,
  });

  /// Pinned cluster id.
  final String clusterId;

  /// Active namespace; used to filter namespaced stores. Empty means
  /// the wizard hasn't picked one yet — the picker renders a hint
  /// instead of fetching, since "SecretStore in (empty)" makes no sense.
  final String namespace;

  /// Currently selected store (or null when nothing picked).
  final StoreSelection? selected;

  final ValueChanged<StoreSelection> onChanged;
  final String? label;
  final String? errorMessage;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;

    if (namespace.trim().isEmpty) {
      return _frame(
        colors,
        Text(
          'Pick a namespace first to load SecretStores.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
      );
    }

    final async = ref.watch(storeListProvider(
      StoreListKey(clusterId: clusterId, namespace: namespace.trim()),
    ));

    return async.when(
      loading: () => _frame(
        colors,
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 12),
          child: LinearProgressIndicator(minHeight: 2),
        ),
      ),
      error: (e, _) => _frame(
        colors,
        Text(
          'Failed to load stores: $e',
          style: TextStyle(color: colors.error, fontSize: 12),
        ),
      ),
      data: (lists) {
        final hasAny = lists.namespaced.isNotEmpty || lists.cluster.isNotEmpty;
        if (!hasAny) {
          return _frame(
            colors,
            Text(
              'No SecretStores in "$namespace" and no ClusterSecretStores '
              'visible. Create a SecretStore first, or pick a different '
              'namespace.',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          );
        }

        final items = <DropdownMenuItem<StoreSelection>>[];
        for (final s in lists.namespaced) {
          items.add(DropdownMenuItem(
            value: StoreSelection(name: s.name, kind: 'SecretStore'),
            child: _row(colors, s),
          ));
        }
        for (final s in lists.cluster) {
          items.add(DropdownMenuItem(
            value: StoreSelection(name: s.name, kind: 'ClusterSecretStore'),
            child: _row(colors, s),
          ));
        }

        // If [selected] points at an entry not in the loaded lists
        // (RBAC-hidden, just deleted, etc.) surface it as an extra item
        // so the dropdown can render a value rather than asserting.
        final allNames = {
          ...lists.namespaced.map((s) => s.name),
          ...lists.cluster.map((s) => s.name),
        };
        if (selected != null && !allNames.contains(selected!.name)) {
          items.insert(
            0,
            DropdownMenuItem(
              value: selected,
              child: _row(
                colors,
                StoreOption(
                  name: selected!.name,
                  kind: selected!.kind,
                  provider: '',
                ),
              ),
            ),
          );
        }

        return DropdownButtonFormField<StoreSelection>(
          initialValue: selected,
          isExpanded: true,
          decoration: InputDecoration(
            labelText: label,
            border: const OutlineInputBorder(),
            errorText: errorMessage,
          ),
          items: items,
          onChanged: (v) {
            if (v != null) onChanged(v);
          },
        );
      },
    );
  }

  Widget _row(KubeColors colors, StoreOption s) {
    final isCluster = s.kind == 'ClusterSecretStore';
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          isCluster ? Icons.public : Icons.account_tree_outlined,
          size: 16,
          color: isCluster ? colors.accent : colors.textSecondary,
        ),
        const SizedBox(width: 8),
        Flexible(
          child: Text(
            s.name,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(color: colors.textPrimary),
          ),
        ),
        const SizedBox(width: 8),
        Text(
          s.provider.isEmpty ? s.kind : '${s.kind} · ${s.provider}',
          style: TextStyle(color: colors.textMuted, fontSize: 11),
        ),
      ],
    );
  }

  Widget _frame(KubeColors colors, Widget child) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (label != null) ...[
            Text(
              label!,
              style: TextStyle(
                color: colors.textMuted,
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 4),
          ],
          child,
          if (errorMessage != null) ...[
            const SizedBox(height: 6),
            Text(
              errorMessage!,
              style: TextStyle(color: colors.error, fontSize: 12),
            ),
          ],
        ],
      ),
    );
  }
}
