// ExternalSecrets list — every ESO ExternalSecret the active user can
// read across all namespaces. Tap row → detail screen (which fetches
// live `driftStatus`; this screen renders only the poller's
// `lastObservedDriftStatus`).
//
// Filter chips (All / SyncFailed / Stale / Drifted / Unknown) + free-
// text search. `?status=*` URL param seeds the chip on mount.
//
// Status gating: `esoStatusProvider` gates the surface — `FeatureUnavailableState.eso()`
// when ESO isn't detected.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/eso_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/refresh_guard.dart';
import 'eso_widgets.dart';

/// Filter chip values for the ExternalSecrets list. Mirrors the
/// failure-table severity grouping on the dashboard so chip-based
/// navigation from a dashboard summary stays consistent.
enum EsListFilter { all, syncFailed, stale, drifted, unknown }

EsListFilter _filterFromInitial(String? initial) {
  switch (initial) {
    case 'syncfailed':
    case 'syncFailed':
    case 'sync-failed':
      return EsListFilter.syncFailed;
    case 'stale':
      return EsListFilter.stale;
    case 'drifted':
      return EsListFilter.drifted;
    case 'unknown':
      return EsListFilter.unknown;
    default:
      return EsListFilter.all;
  }
}

class ExternalSecretsListScreen extends ConsumerStatefulWidget {
  const ExternalSecretsListScreen({super.key, this.initialStatusFilter});

  /// Seed the status chip on mount. Drawer deep-links leave this null;
  /// dashboard-card taps (or expiry-style push deep links) pass a value.
  final String? initialStatusFilter;

  @override
  ConsumerState<ExternalSecretsListScreen> createState() =>
      _ExternalSecretsListScreenState();
}

class _ExternalSecretsListScreenState
    extends ConsumerState<ExternalSecretsListScreen> {
  late EsListFilter _filter;
  String _search = '';
  final _searchCtl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _filter = _filterFromInitial(widget.initialStatusFilter);
  }

  @override
  void dispose() {
    _searchCtl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<String>(activeClusterProvider, (previous, next) {
      if (previous != next) {
        setState(() {
          _filter = EsListFilter.all;
          _search = '';
          _searchCtl.clear();
        });
      }
    });

    return Scaffold(
      appBar: AppBar(title: const Text('ExternalSecrets')),
      body: EsoStatusGate(
        builder: (clusterId) => _ListBody(
          clusterId: clusterId,
          filter: _filter,
          search: _search,
          searchCtl: _searchCtl,
          onFilterChanged: (v) => setState(() => _filter = v),
          onSearchChanged: (v) => setState(() => _search = v),
        ),
      ),
    );
  }
}

class _ListBody extends ConsumerStatefulWidget {
  const _ListBody({
    required this.clusterId,
    required this.filter,
    required this.search,
    required this.searchCtl,
    required this.onFilterChanged,
    required this.onSearchChanged,
  });

  final String clusterId;
  final EsListFilter filter;
  final String search;
  final TextEditingController searchCtl;
  final ValueChanged<EsListFilter> onFilterChanged;
  final ValueChanged<String> onSearchChanged;

  @override
  ConsumerState<_ListBody> createState() => _ListBodyState();
}

class _ListBodyState extends ConsumerState<_ListBody> with RefreshGuardMixin {
  // Cached lower-cased search haystack ("name namespace storeName") parallel
  // to [_lastItems]. Rebuilt only when the items list reference changes —
  // the typical keystroke path reuses the cache, dropping the per-keystroke
  // cost from O(N) `toLowerCase` calls to a constant array lookup.
  List<ExternalSecret>? _lastItems;
  List<String>? _haystacks;

  ExternalSecretListKey get _key =>
      ExternalSecretListKey(clusterId: widget.clusterId);

  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(externalSecretListProvider(_key));
        try {
          await ref.read(externalSecretListProvider(_key).future);
        } on Object {
          // surfaces via .when
        }
      });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(externalSecretListProvider(_key));

    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load ExternalSecrets',
          error: e,
          onRetry: _handleRefresh,
        ),
        data: (items) {
          final filtered = _applyFilters(items);
          return CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: _FilterStrip(
                  filter: widget.filter,
                  searchCtl: widget.searchCtl,
                  totalVisible: filtered.length,
                  totalAll: items.length,
                  onFilterChanged: widget.onFilterChanged,
                  onSearchChanged: widget.onSearchChanged,
                ),
              ),
              if (filtered.isEmpty)
                SliverToBoxAdapter(
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 16,
                      vertical: 32,
                    ),
                    child: Center(
                      child: Text(
                        items.isEmpty
                            ? 'No ExternalSecrets in this cluster yet. The '
                                'ESO controller will populate the list once '
                                'an ExternalSecret resource is created.'
                            : 'No ExternalSecrets match the current filters.',
                        style: TextStyle(color: colors.textMuted),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                )
              else
                SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) => _EsRow(
                      cert: filtered[index],
                      onTap: () => context.push(
                        '/clusters/${widget.clusterId}/eso/externalsecrets/'
                        '${Uri.encodeComponent(filtered[index].namespace)}/'
                        '${Uri.encodeComponent(filtered[index].name)}',
                      ),
                    ),
                    childCount: filtered.length,
                  ),
                ),
              const SliverPadding(padding: EdgeInsets.only(bottom: 8)),
            ],
          );
        },
      ),
    );
  }

  List<ExternalSecret> _applyFilters(List<ExternalSecret> items) {
    final q = widget.search.trim().toLowerCase();

    // Refresh the haystack cache only when the underlying items list
    // changes identity (refresh, cluster switch). Keystroke rebuilds keep
    // the cache.
    if (!identical(_lastItems, items)) {
      _lastItems = items;
      _haystacks = List<String>.generate(
        items.length,
        (i) {
          final es = items[i];
          return '${es.name} ${es.namespace} ${es.storeRef.name}'
              .toLowerCase();
        },
        growable: false,
      );
    }

    final haystacks = _haystacks!;
    final out = <ExternalSecret>[];
    for (var i = 0; i < items.length; i++) {
      final es = items[i];
      switch (widget.filter) {
        case EsListFilter.syncFailed:
          if (es.status != EsoStatus.syncFailed) continue;
          break;
        case EsListFilter.stale:
          if (es.status != EsoStatus.stale) continue;
          break;
        case EsListFilter.drifted:
          if (es.status != EsoStatus.drifted) continue;
          break;
        case EsListFilter.unknown:
          if (es.status != EsoStatus.unknown) continue;
          break;
        case EsListFilter.all:
          break;
      }
      if (q.isEmpty || haystacks[i].contains(q)) {
        out.add(es);
      }
    }
    return out;
  }
}

class _FilterStrip extends StatelessWidget {
  const _FilterStrip({
    required this.filter,
    required this.searchCtl,
    required this.totalVisible,
    required this.totalAll,
    required this.onFilterChanged,
    required this.onSearchChanged,
  });

  final EsListFilter filter;
  final TextEditingController searchCtl;
  final int totalVisible;
  final int totalAll;
  final ValueChanged<EsListFilter> onFilterChanged;
  final ValueChanged<String> onSearchChanged;

  String _label(EsListFilter v) => switch (v) {
        EsListFilter.all => 'All',
        EsListFilter.syncFailed => 'SyncFailed',
        EsListFilter.stale => 'Stale',
        EsListFilter.drifted => 'Drifted',
        EsListFilter.unknown => 'Unknown',
      };

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(
            spacing: 6,
            runSpacing: 4,
            children: [
              for (final value in EsListFilter.values)
                ChoiceChip(
                  label: Text(_label(value)),
                  selected: value == filter,
                  onSelected: (_) => onFilterChanged(value),
                ),
            ],
          ),
          const SizedBox(height: 8),
          TextField(
            controller: searchCtl,
            decoration: InputDecoration(
              hintText: 'Filter by name, namespace, store…',
              prefixIcon: Icon(
                Icons.search,
                size: 18,
                color: colors.textMuted,
              ),
              isDense: true,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(6),
              ),
            ),
            onChanged: onSearchChanged,
          ),
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
              '$totalVisible of $totalAll ExternalSecrets',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}

class _EsRow extends StatelessWidget {
  const _EsRow({required this.cert, required this.onTap});

  final ExternalSecret cert;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final drift = cert.effectiveDriftStatus;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    cert.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 15,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: 8),
                EsoStatusPill(status: cert.status, dense: true),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              '${cert.namespace} · ${cert.storeRef.kind}/${cert.storeRef.name}',
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
              overflow: TextOverflow.ellipsis,
            ),
            if (cert.refreshInterval != null) ...[
              const SizedBox(height: 2),
              Text(
                'refresh every ${cert.refreshInterval}',
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ],
            const SizedBox(height: 6),
            Row(
              children: [
                if (drift != DriftStatus.notObserved) ...[
                  DriftPill(
                    status: drift,
                    reason: cert.driftUnknownReason,
                    dense: true,
                  ),
                  const SizedBox(width: 6),
                ],
                if (cert.targetSecretName != null)
                  Flexible(
                    child: Text(
                      '→ ${cert.targetSecretName}',
                      style: TextStyle(color: colors.textMuted, fontSize: 11),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                const Spacer(),
                Icon(
                  Icons.chevron_right,
                  size: 16,
                  color: colors.textMuted,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
