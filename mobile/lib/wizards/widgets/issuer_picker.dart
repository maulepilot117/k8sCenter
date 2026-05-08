// Combined Issuer + ClusterIssuer picker. Used by the Certificate wizard
// to select a `spec.issuerRef`. Surfaces both namespace-scoped issuers
// (filtered to the wizard's active namespace) and cluster-scoped issuers
// in a single dropdown, distinguished by a leading icon so the operator
// sees scope at a glance.
//
// Backend endpoints:
//   GET /api/v1/certificates/issuers           → all visible issuers (RBAC-filtered)
//   GET /api/v1/certificates/clusterissuers    → all cluster issuers
//
// The list-issuers endpoint returns issuers across every namespace the
// operator has read on; we filter client-side to [namespace] so the
// operator only sees issuers that can sign a Certificate in the active
// namespace. (cert-manager itself rejects cross-namespace Issuer refs.)
//
// Cluster pinning: clusterId is threaded through as an X-Cluster-ID
// header so a mid-flight switch of activeClusterProvider can't redirect
// the fetch to a different cluster's issuers — same defense pattern as
// PR-3c's NamedResourcePicker. The interceptor only injects when the
// header is absent; we always set it here, so the request always lands
// on the pinned cluster.
//
// Selection emits a typed [IssuerSelection] carrying both the name and
// the kind ("Issuer" or "ClusterIssuer"). The wizard form needs both —
// kind drives whether spec.issuerRef.kind is Issuer or ClusterIssuer.

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/dio_client.dart';
import '../../theme/kube_theme_builder.dart';

/// One entry in the combined picker. Carries the name and the scope
/// (namespaced Issuer or cluster-wide ClusterIssuer) so the wizard can
/// fill both `issuerRef.name` and `issuerRef.kind`.
class IssuerSelection {
  const IssuerSelection({required this.name, required this.kind});

  /// Issuer / ClusterIssuer metadata.name.
  final String name;

  /// "Issuer" or "ClusterIssuer". Matches cert-manager's Kind values
  /// and the backend's CertificateIssuerRefInput.Kind expectation.
  final String kind;

  @override
  bool operator ==(Object other) =>
      other is IssuerSelection && other.name == name && other.kind == kind;

  @override
  int get hashCode => Object.hash(name, kind);
}

/// Compound result from the two list endpoints. Each list is a plain
/// [String] (issuer name); the picker merges them into typed entries.
class _IssuerLists {
  const _IssuerLists({required this.namespaced, required this.cluster});
  final List<String> namespaced;
  final List<String> cluster;
}

/// Family key — clusterId pins the cache slot, namespace scopes the
/// namespaced-issuer filter. Cluster-scoped issuers are always included.
class IssuerListKey {
  const IssuerListKey({required this.clusterId, required this.namespace});

  final String clusterId;
  final String namespace;

  @override
  bool operator ==(Object other) =>
      other is IssuerListKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace;

  @override
  int get hashCode => Object.hash(clusterId, namespace);
}

/// Fetches issuers + cluster-issuers in parallel, filtering namespaced
/// issuers to [namespace]. Returns empty lists when cert-manager isn't
/// installed (the backend returns `data: []` with 200 in that case).
final issuerListProvider = FutureProvider.autoDispose
    .family<_IssuerLists, IssuerListKey>((ref, key) async {
  final dio = ref.watch(dioProvider);
  final headers = {'X-Cluster-ID': key.clusterId};

  Future<List<dynamic>> fetch(String path) async {
    try {
      final res = await dio.get<Map<String, dynamic>>(
        path,
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
    fetch('/api/v1/certificates/issuers'),
    fetch('/api/v1/certificates/clusterissuers'),
  ]);

  // Namespaced issuers come back across every namespace the operator
  // can read; filter to the wizard's namespace so cross-namespace
  // entries (which cert-manager would reject anyway) don't pollute the
  // dropdown.
  final namespaced = <String>{};
  for (final item in results[0]) {
    if (item is! Map) continue;
    final ns = item['namespace'];
    final name = item['name'];
    if (ns is String && name is String && ns == key.namespace && name.isNotEmpty) {
      namespaced.add(name);
    }
  }
  final cluster = <String>{};
  for (final item in results[1]) {
    if (item is! Map) continue;
    final name = item['name'];
    if (name is String && name.isNotEmpty) cluster.add(name);
  }

  return _IssuerLists(
    namespaced: namespaced.toList()..sort(),
    cluster: cluster.toList()..sort(),
  );
});

class IssuerPicker extends ConsumerWidget {
  const IssuerPicker({
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

  /// Active namespace; used to filter namespaced issuers. Empty means
  /// the wizard hasn't picked one yet — the picker renders a hint
  /// instead of fetching, since "Issuer in (empty)" makes no sense.
  final String namespace;

  /// Currently selected issuer (or null when nothing picked).
  final IssuerSelection? selected;

  final ValueChanged<IssuerSelection> onChanged;
  final String? label;
  final String? errorMessage;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;

    if (namespace.trim().isEmpty) {
      return _frame(
        colors,
        Text(
          'Pick a namespace first to load Issuers.',
          style: TextStyle(color: colors.textMuted, fontSize: 12),
        ),
      );
    }

    final async = ref.watch(issuerListProvider(
      IssuerListKey(clusterId: clusterId, namespace: namespace.trim()),
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
          'Failed to load issuers: $e',
          style: TextStyle(color: colors.error, fontSize: 12),
        ),
      ),
      data: (lists) {
        final hasAny = lists.namespaced.isNotEmpty || lists.cluster.isNotEmpty;
        if (!hasAny) {
          return _frame(
            colors,
            Text(
              'No Issuers in "$namespace" and no ClusterIssuers visible. '
              'Create an Issuer first, or pick a different namespace.',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          );
        }

        final items = <DropdownMenuItem<IssuerSelection>>[];
        for (final n in lists.namespaced) {
          items.add(DropdownMenuItem(
            value: IssuerSelection(name: n, kind: 'Issuer'),
            child: _row(colors, n, 'Issuer'),
          ));
        }
        for (final n in lists.cluster) {
          items.add(DropdownMenuItem(
            value: IssuerSelection(name: n, kind: 'ClusterIssuer'),
            child: _row(colors, n, 'ClusterIssuer'),
          ));
        }

        // If [selected] points at an entry not in the loaded lists
        // (unlikely under steady state, but possible if the operator
        // typed in a name via a future YAML escape hatch and we then
        // open the picker), surface it as an extra item so the dropdown
        // can render a value.
        if (selected != null &&
            !lists.namespaced.contains(selected!.name) &&
            !lists.cluster.contains(selected!.name)) {
          items.insert(
            0,
            DropdownMenuItem(
              value: selected,
              child: _row(colors, selected!.name, selected!.kind),
            ),
          );
        }

        return DropdownButtonFormField<IssuerSelection>(
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

  Widget _row(KubeColors colors, String name, String kind) {
    final isCluster = kind == 'ClusterIssuer';
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
            name,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(color: colors.textPrimary),
          ),
        ),
        const SizedBox(width: 8),
        Text(
          kind,
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
