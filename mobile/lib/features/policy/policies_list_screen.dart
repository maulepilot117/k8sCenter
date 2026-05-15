// Policies list — every normalized policy across both engines, filtered
// by chips (engine / severity / blocking) + free-text search.
//
// Engine availability: if a policy reports `engine: kyverno` but the
// cluster only has Gatekeeper installed (no `kyvernoAvailable`), the row
// surfaces an "Engine not installed" tooltip on the badge. PR-3f's
// engine-intersection learning generalises: the policy CRD could exist
// on the cluster even when the engine pod is unhealthy or absent.
//
// Status gating: `PolicyStatusGate` gates the surface —
// `FeatureUnavailableState.policy()` when neither engine is detected.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/policy_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/refresh_guard.dart';
import 'policy_widgets.dart';

/// Engine filter chip values. Mirrors web's PolicyDashboard filterEngine.
enum PolicyEngineFilter { all, kyverno, gatekeeper }

/// Blocking filter chip values. `all` plus the two action modes.
enum PolicyBlockingFilter { all, blocking, audit }

class PoliciesListScreen extends ConsumerStatefulWidget {
  const PoliciesListScreen({super.key});

  @override
  ConsumerState<PoliciesListScreen> createState() =>
      _PoliciesListScreenState();
}

class _PoliciesListScreenState extends ConsumerState<PoliciesListScreen> {
  PolicyEngineFilter _engineFilter = PolicyEngineFilter.all;
  PolicySeverityFilter _severityFilter = PolicySeverityFilter.all;
  PolicyBlockingFilter _blockingFilter = PolicyBlockingFilter.all;
  String _search = '';
  final _searchCtl = TextEditingController();

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
          _engineFilter = PolicyEngineFilter.all;
          _severityFilter = PolicySeverityFilter.all;
          _blockingFilter = PolicyBlockingFilter.all;
          _search = '';
          _searchCtl.clear();
        });
      }
    });

    return Scaffold(
      appBar: AppBar(title: const Text('Policies')),
      body: PolicyStatusGate(
        builder: (clusterId, status) => _ListBody(
          clusterId: clusterId,
          status: status,
          engineFilter: _engineFilter,
          severityFilter: _severityFilter,
          blockingFilter: _blockingFilter,
          search: _search,
          searchCtl: _searchCtl,
          onEngineChanged: (v) => setState(() => _engineFilter = v),
          onSeverityChanged: (v) => setState(() => _severityFilter = v),
          onBlockingChanged: (v) => setState(() => _blockingFilter = v),
          onSearchChanged: (v) => setState(() => _search = v),
        ),
      ),
    );
  }
}

class _ListBody extends ConsumerStatefulWidget {
  const _ListBody({
    required this.clusterId,
    required this.status,
    required this.engineFilter,
    required this.severityFilter,
    required this.blockingFilter,
    required this.search,
    required this.searchCtl,
    required this.onEngineChanged,
    required this.onSeverityChanged,
    required this.onBlockingChanged,
    required this.onSearchChanged,
  });

  final String clusterId;
  final PolicyDiscoveryStatus status;
  final PolicyEngineFilter engineFilter;
  final PolicySeverityFilter severityFilter;
  final PolicyBlockingFilter blockingFilter;
  final String search;
  final TextEditingController searchCtl;
  final ValueChanged<PolicyEngineFilter> onEngineChanged;
  final ValueChanged<PolicySeverityFilter> onSeverityChanged;
  final ValueChanged<PolicyBlockingFilter> onBlockingChanged;
  final ValueChanged<String> onSearchChanged;

  @override
  ConsumerState<_ListBody> createState() => _ListBodyState();
}

class _ListBodyState extends ConsumerState<_ListBody> with RefreshGuardMixin {
  // Cached lower-cased search haystack ("name kind category description")
  // parallel to [_lastItems]. Rebuilt only when the items list reference
  // changes — keystroke rebuilds reuse the cache, dropping the per-
  // keystroke cost from O(N) `toLowerCase` calls to a constant array
  // lookup. Same pattern as ExternalSecretsListScreen.
  List<PolicyItem>? _lastItems;
  List<String>? _haystacks;

  // Filtered result cache: (items, engine, severity, blocking, search)
  // → filtered list. Invalidated whenever any cache-key field changes.
  // Without this cache, each build allocates a fresh list and re-runs
  // 4 predicate checks across every row.
  List<PolicyItem>? _cachedFiltered;
  PolicyEngineFilter? _cachedFilterEngine;
  PolicySeverityFilter? _cachedFilterSeverity;
  PolicyBlockingFilter? _cachedFilterBlocking;
  String? _cachedFilterSearch;

  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(policiesListProvider(widget.clusterId));
        try {
          await ref.read(policiesListProvider(widget.clusterId).future);
        } on Object {
          // surfaces via .when
        }
      });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(policiesListProvider(widget.clusterId));

    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load policies',
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
                  engineFilter: widget.engineFilter,
                  severityFilter: widget.severityFilter,
                  blockingFilter: widget.blockingFilter,
                  searchCtl: widget.searchCtl,
                  totalVisible: filtered.length,
                  totalAll: items.length,
                  onEngineChanged: widget.onEngineChanged,
                  onSeverityChanged: widget.onSeverityChanged,
                  onBlockingChanged: widget.onBlockingChanged,
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
                            ? 'No policies defined on this cluster. The '
                                'policy engine will populate the list once '
                                'a ClusterPolicy or ConstraintTemplate is '
                                'created.'
                            : 'No policies match the current filters.',
                        style: TextStyle(color: colors.textMuted),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                )
              else
                SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) => _PolicyRow(
                      policy: filtered[index],
                      engineAvailable:
                          _isEngineAvailable(filtered[index].engine),
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

  bool _isEngineAvailable(PolicyEngine engine) {
    switch (engine) {
      case PolicyEngine.kyverno:
        return widget.status.kyvernoAvailable;
      case PolicyEngine.gatekeeper:
        return widget.status.gatekeeperAvailable;
      case PolicyEngine.unknown:
        // Unknown engines render with no availability tooltip — there is
        // no install action we can suggest for an engine we don't recognise.
        return true;
    }
  }

  List<PolicyItem> _applyFilters(List<PolicyItem> items) {
    final q = widget.search.trim().toLowerCase();

    // Refresh the haystack cache only when the underlying items list
    // changes identity (refresh, cluster switch). Invalidate downstream
    // filtered-result cache at the same time.
    if (!identical(_lastItems, items)) {
      _lastItems = items;
      _haystacks = items
          .map((p) => [
                p.name,
                p.kind,
                p.namespace,
                p.category ?? '',
                p.description ?? '',
              ].join(' ').toLowerCase())
          .toList();
      _cachedFiltered = null;
    }

    // Reuse the previous filtered result when nothing relevant changed.
    if (_cachedFiltered != null &&
        _cachedFilterEngine == widget.engineFilter &&
        _cachedFilterSeverity == widget.severityFilter &&
        _cachedFilterBlocking == widget.blockingFilter &&
        _cachedFilterSearch == q) {
      return _cachedFiltered!;
    }

    final hs = _haystacks!;
    final out = <PolicyItem>[];
    for (var i = 0; i < items.length; i++) {
      final p = items[i];
      if (!_matchesEngine(p)) continue;
      if (!_matchesSeverity(p)) continue;
      if (!_matchesBlocking(p)) continue;
      if (q.isNotEmpty && !hs[i].contains(q)) continue;
      out.add(p);
    }

    _cachedFiltered = out;
    _cachedFilterEngine = widget.engineFilter;
    _cachedFilterSeverity = widget.severityFilter;
    _cachedFilterBlocking = widget.blockingFilter;
    _cachedFilterSearch = q;
    return out;
  }

  bool _matchesEngine(PolicyItem p) => switch (widget.engineFilter) {
        PolicyEngineFilter.all => true,
        PolicyEngineFilter.kyverno => p.engine == PolicyEngine.kyverno,
        PolicyEngineFilter.gatekeeper => p.engine == PolicyEngine.gatekeeper,
      };

  bool _matchesSeverity(PolicyItem p) => switch (widget.severityFilter) {
        PolicySeverityFilter.all => true,
        PolicySeverityFilter.critical => p.severity == 'critical',
        PolicySeverityFilter.high => p.severity == 'high',
        PolicySeverityFilter.medium => p.severity == 'medium',
        PolicySeverityFilter.low => p.severity == 'low',
      };

  bool _matchesBlocking(PolicyItem p) => switch (widget.blockingFilter) {
        PolicyBlockingFilter.all => true,
        PolicyBlockingFilter.blocking => p.blocking,
        PolicyBlockingFilter.audit => !p.blocking,
      };
}

// ---------------------------------------------------------------------------
// Filter strip
// ---------------------------------------------------------------------------

class _FilterStrip extends StatelessWidget {
  const _FilterStrip({
    required this.engineFilter,
    required this.severityFilter,
    required this.blockingFilter,
    required this.searchCtl,
    required this.totalVisible,
    required this.totalAll,
    required this.onEngineChanged,
    required this.onSeverityChanged,
    required this.onBlockingChanged,
    required this.onSearchChanged,
  });

  final PolicyEngineFilter engineFilter;
  final PolicySeverityFilter severityFilter;
  final PolicyBlockingFilter blockingFilter;
  final TextEditingController searchCtl;
  final int totalVisible;
  final int totalAll;
  final ValueChanged<PolicyEngineFilter> onEngineChanged;
  final ValueChanged<PolicySeverityFilter> onSeverityChanged;
  final ValueChanged<PolicyBlockingFilter> onBlockingChanged;
  final ValueChanged<String> onSearchChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          TextField(
            controller: searchCtl,
            onChanged: onSearchChanged,
            decoration: InputDecoration(
              prefixIcon: const Icon(Icons.search, size: 18),
              hintText: 'Search policies',
              isDense: true,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(6),
              ),
            ),
          ),
          const SizedBox(height: 8),
          // Filter chip groups — record-loop pattern keeps the three
          // groups (engine / severity / blocking) symmetric with
          // violations_list_screen.dart's inline form rather than
          // splaying out a private wrapper class per enum.
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                for (final tuple in [
                  (PolicyEngineFilter.all, 'All'),
                  (PolicyEngineFilter.kyverno, 'Kyverno'),
                  (PolicyEngineFilter.gatekeeper, 'Gatekeeper'),
                ])
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: ChoiceChip(
                      selected: tuple.$1 == engineFilter,
                      label: Text(tuple.$2),
                      onSelected: (_) => onEngineChanged(tuple.$1),
                    ),
                  ),
                const SizedBox(width: 12),
                for (final tuple in [
                  (PolicySeverityFilter.all, 'Any severity'),
                  (PolicySeverityFilter.critical, 'Critical'),
                  (PolicySeverityFilter.high, 'High'),
                  (PolicySeverityFilter.medium, 'Medium'),
                  (PolicySeverityFilter.low, 'Low'),
                ])
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: ChoiceChip(
                      selected: tuple.$1 == severityFilter,
                      label: Text(tuple.$2),
                      onSelected: (_) => onSeverityChanged(tuple.$1),
                    ),
                  ),
                const SizedBox(width: 12),
                for (final tuple in [
                  (PolicyBlockingFilter.all, 'All actions'),
                  (PolicyBlockingFilter.blocking, 'Blocking'),
                  (PolicyBlockingFilter.audit, 'Audit'),
                ])
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: ChoiceChip(
                      selected: tuple.$1 == blockingFilter,
                      label: Text(tuple.$2),
                      onSelected: (_) => onBlockingChanged(tuple.$1),
                    ),
                  ),
              ],
            ),
          ),
          const SizedBox(height: 4),
          Text(
            totalVisible == totalAll
                ? '$totalAll ${totalAll == 1 ? 'policy' : 'policies'}'
                : 'Showing $totalVisible of $totalAll policies',
            style: TextStyle(color: colors.textMuted, fontSize: 11),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Row
// ---------------------------------------------------------------------------

class _PolicyRow extends StatelessWidget {
  const _PolicyRow({required this.policy, required this.engineAvailable});

  final PolicyItem policy;
  final bool engineAvailable;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: colors.borderSubtle)),
      ),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  policy.name,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 2),
                Wrap(
                  spacing: 6,
                  runSpacing: 4,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    EngineBadge(
                      engine: policy.engine,
                      dense: true,
                      showUnavailableHint: !engineAvailable,
                    ),
                    SeverityBadge(severity: policy.severity, dense: true),
                    BlockingBadge(blocking: policy.blocking, dense: true),
                    if (policy.violationCount > 0)
                      Text(
                        '${policy.violationCount} '
                        '${policy.violationCount == 1 ? 'violation' : 'violations'}',
                        style: TextStyle(
                          color: colors.error,
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                  ],
                ),
                if (policy.description != null &&
                    policy.description!.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 4),
                    child: Text(
                      policy.description!,
                      style: TextStyle(color: colors.textMuted, fontSize: 11),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
