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

/// Compound result from the two list endpoints. Each fetch can fail
/// independently — when one succeeds and the other doesn't, we surface
/// the partial result rather than blocking the picker entirely.
/// `errors` carries one entry per failed kind ("namespaced" or
/// "cluster") so the picker can render an inline warning above the
/// dropdown.
class _StoreLists {
  const _StoreLists({
    required this.namespaced,
    required this.cluster,
    this.errors = const <String, String>{},
  });
  final List<StoreOption> namespaced;
  final List<StoreOption> cluster;
  final Map<String, String> errors;
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

  // Each fetch returns either a list or a one-line error message.
  // We catch per-fetch so a transient failure on one endpoint doesn't
  // gate the operator out of the other endpoint's results.
  Future<({List<dynamic> data, String? error})> fetch(String path,
      {Map<String, dynamic>? query}) async {
    try {
      final res = await dio.get<Map<String, dynamic>>(
        path,
        queryParameters: query,
        options: Options(headers: headers),
      );
      final data = res.data?['data'];
      return (data: data is List ? data : const <dynamic>[], error: null);
    } on DioException catch (e) {
      final err = e.error;
      final apiErr = err is ApiError ? err : ApiError.fromDio(e);
      return (data: const <dynamic>[], error: apiErr.message);
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
  for (final item in results[0].data) {
    final opt = toOption(item, 'SecretStore');
    if (opt != null) namespaced.add(opt);
  }
  final cluster = <StoreOption>[];
  for (final item in results[1].data) {
    final opt = toOption(item, 'ClusterSecretStore');
    if (opt != null) cluster.add(opt);
  }
  namespaced.sort((a, b) => a.name.compareTo(b.name));
  cluster.sort((a, b) => a.name.compareTo(b.name));
  final errors = <String, String>{};
  if (results[0].error != null) errors['namespaced'] = results[0].error!;
  if (results[1].error != null) errors['cluster'] = results[1].error!;
  return _StoreLists(namespaced: namespaced, cluster: cluster, errors: errors);
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
        // (RBAC-hidden, just deleted, or vanished after a namespace
        // change), surface it as an extra dropdown item so the value
        // renders, AND warn the operator above the dropdown so they
        // know the store reference will fail at apply time. Without
        // the warning, the wizard happily ships a YAML referencing a
        // non-existent store; ESO reconciliation lands NotReady
        // forever and the operator has no surface to learn why.
        final allNames = {
          ...lists.namespaced.map((s) => s.name),
          ...lists.cluster.map((s) => s.name),
        };
        final selectedMissing =
            selected != null && !allNames.contains(selected!.name);
        if (selectedMissing) {
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

        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Surface any partial-fetch error so the operator knows
            // their list of options is incomplete (one of the two
            // endpoints failed but the other returned).
            if (lists.errors.isNotEmpty) ...[
              _PartialFetchWarning(errors: lists.errors, colors: colors),
              const SizedBox(height: 8),
            ],
            if (selectedMissing) ...[
              _SelectedMissingWarning(
                name: selected!.name,
                kind: selected!.kind,
                colors: colors,
              ),
              const SizedBox(height: 8),
            ],
            DropdownButtonFormField<StoreSelection>(
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
            ),
          ],
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

  // Warning widgets are private to this file; they only render when
  // their respective conditions hold (partial-fetch failure /
  // selection vanished from the visible list).
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

class _PartialFetchWarning extends StatelessWidget {
  const _PartialFetchWarning({required this.errors, required this.colors});
  final Map<String, String> errors;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final missing = errors.keys
        .map((k) => k == 'namespaced' ? 'SecretStores' : 'ClusterSecretStores')
        .join(' + ');
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        border: Border.all(color: colors.warning),
        borderRadius: BorderRadius.circular(4),
        color: colors.warning.withValues(alpha: 0.08),
      ),
      child: Row(children: [
        Icon(Icons.warning_amber_outlined, size: 14, color: colors.warning),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            'Failed to load $missing — list is incomplete',
            style: TextStyle(color: colors.textSecondary, fontSize: 11),
          ),
        ),
      ]),
    );
  }
}

class _SelectedMissingWarning extends StatelessWidget {
  const _SelectedMissingWarning({
    required this.name,
    required this.kind,
    required this.colors,
  });
  final String name;
  final String kind;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        border: Border.all(color: colors.warning),
        borderRadius: BorderRadius.circular(4),
        color: colors.warning.withValues(alpha: 0.08),
      ),
      child: Row(children: [
        Icon(Icons.warning_amber_outlined, size: 14, color: colors.warning),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            '$kind "$name" is no longer visible — it may have been '
            'deleted or you lost RBAC. Pick a different store before '
            'applying.',
            style: TextStyle(color: colors.textSecondary, fontSize: 11),
          ),
        ),
      ]),
    );
  }
}
