// Violations list — every policy violation the active user can read
// (RBAC-filtered server-side by `list pods` namespace access). Filter
// chips on namespace + severity; free-text search across policy / rule
// / target name. Virtual scroll via `SliverList.builder` to handle
// 1000+-violation responses on busy clusters.
//
// Tapping a row routes to the detail screen at
// `/clusters/<id>/policy/violations/<stableKey>`. The stable key is
// `policy|rule|namespace|kind|name` — there is no server-side id field
// on violations, so this is the only durable identifier.
//
// Status gating: `PolicyStatusGate` gates the surface —
// `FeatureUnavailableState.policy()` when neither engine is detected.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/policy_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/refresh_guard.dart';
import 'policy_widgets.dart';

class ViolationsListScreen extends ConsumerStatefulWidget {
  const ViolationsListScreen({super.key, this.initialNamespace});

  /// Seed the namespace filter on mount. Drawer deep-links leave this
  /// null; deep-links from a workload's namespace summary pass a value.
  final String? initialNamespace;

  @override
  ConsumerState<ViolationsListScreen> createState() =>
      _ViolationsListScreenState();
}

class _ViolationsListScreenState extends ConsumerState<ViolationsListScreen> {
  late String _namespaceFilter;
  PolicySeverityFilter _severityFilter = PolicySeverityFilter.all;
  String _search = '';
  final _searchCtl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _namespaceFilter = widget.initialNamespace ?? '';
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
          _namespaceFilter = '';
          _severityFilter = PolicySeverityFilter.all;
          _search = '';
          _searchCtl.clear();
        });
      }
    });

    return Scaffold(
      appBar: AppBar(title: const Text('Violations')),
      body: PolicyStatusGate(
        builder: (clusterId, _) => _ListBody(
          clusterId: clusterId,
          namespaceFilter: _namespaceFilter,
          severityFilter: _severityFilter,
          search: _search,
          searchCtl: _searchCtl,
          onNamespaceChanged: (v) => setState(() => _namespaceFilter = v),
          onSeverityChanged: (v) => setState(() => _severityFilter = v),
          onSearchChanged: (v) => setState(() => _search = v),
        ),
      ),
    );
  }
}

class _ListBody extends ConsumerStatefulWidget {
  const _ListBody({
    required this.clusterId,
    required this.namespaceFilter,
    required this.severityFilter,
    required this.search,
    required this.searchCtl,
    required this.onNamespaceChanged,
    required this.onSeverityChanged,
    required this.onSearchChanged,
  });

  final String clusterId;
  final String namespaceFilter;
  final PolicySeverityFilter severityFilter;
  final String search;
  final TextEditingController searchCtl;
  final ValueChanged<String> onNamespaceChanged;
  final ValueChanged<PolicySeverityFilter> onSeverityChanged;
  final ValueChanged<String> onSearchChanged;

  @override
  ConsumerState<_ListBody> createState() => _ListBodyState();
}

class _ListBodyState extends ConsumerState<_ListBody> with RefreshGuardMixin {
  List<PolicyViolation>? _lastItems;
  List<String>? _haystacks;

  // Distinct namespaces cached against the items reference — only
  // rebuilt when the underlying list identity changes (refresh, cluster
  // switch). Per-keystroke builds reuse the cached list.
  List<String>? _cachedNamespaces;

  // Filtered result cache: (items, namespace, severity, search) →
  // filtered list. Invalidated whenever any cache-key field changes.
  // Without this cache, each rebuild allocates a fresh list and re-runs
  // 3 predicate checks across every row — O(N) on every keystroke, chip
  // tap, or scroll frame.
  List<PolicyViolation>? _cachedFiltered;
  String? _cachedFilterNamespace;
  PolicySeverityFilter? _cachedFilterSeverity;
  String? _cachedFilterSearch;

  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(violationsListProvider(widget.clusterId));
        try {
          await ref.read(violationsListProvider(widget.clusterId).future);
        } on Object {
          // surfaces via .when
        }
      });

  /// Set of distinct namespaces from the unfiltered response. Used to
  /// populate the namespace dropdown. Sorted ascending so the picker
  /// scans alphabetically. Cached against [_lastItems] identity so
  /// keystroke rebuilds don't allocate a fresh Set + sorted List each
  /// frame.
  List<String> _namespaces(List<PolicyViolation> items) {
    if (identical(_lastItems, items) && _cachedNamespaces != null) {
      return _cachedNamespaces!;
    }
    final set = <String>{};
    for (final v in items) {
      if (v.namespace.isNotEmpty) set.add(v.namespace);
    }
    final out = set.toList()..sort();
    _cachedNamespaces = out;
    return out;
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(violationsListProvider(widget.clusterId));

    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load violations',
          error: e,
          onRetry: _handleRefresh,
        ),
        data: (items) {
          final namespaces = _namespaces(items);
          // Reset the namespace filter when the post-refresh data no
          // longer contains the previously-selected namespace (e.g.,
          // every violation in that namespace was remediated). Without
          // this, the dropdown guard would render "All" but the filter
          // would still apply the stale namespace and the list would
          // show a misleading "no violations match" empty state. Defer
          // the reset to post-frame so we don't call setState during
          // build.
          if (widget.namespaceFilter.isNotEmpty &&
              !namespaces.contains(widget.namespaceFilter)) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (mounted) widget.onNamespaceChanged('');
            });
          }
          final filtered = _applyFilters(items);
          return CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: _FilterStrip(
                  namespaceFilter: widget.namespaceFilter,
                  namespaces: namespaces,
                  severityFilter: widget.severityFilter,
                  searchCtl: widget.searchCtl,
                  totalVisible: filtered.length,
                  totalAll: items.length,
                  onNamespaceChanged: widget.onNamespaceChanged,
                  onSeverityChanged: widget.onSeverityChanged,
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
                            ? 'No policy violations reported. The cluster '
                                'is currently compliant with every defined '
                                'policy.'
                            : 'No violations match the current filters.',
                        style: TextStyle(color: colors.textMuted),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                )
              else
                SliverList(
                  // Virtual scroll via SliverChildBuilderDelegate handles
                  // 1000+-violation responses without dropping frames —
                  // only the visible rows are constructed.
                  delegate: SliverChildBuilderDelegate(
                    (context, index) => _ViolationRow(
                      violation: filtered[index],
                      onTap: () => context.push(
                        '/clusters/${widget.clusterId}/policy/violations/'
                        '${Uri.encodeComponent(filtered[index].stableKey)}',
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

  List<PolicyViolation> _applyFilters(List<PolicyViolation> items) {
    final q = widget.search.trim().toLowerCase();

    if (!identical(_lastItems, items)) {
      _lastItems = items;
      _haystacks = items
          .map((v) => [
                v.policy,
                v.rule,
                v.namespace,
                v.kind,
                v.name,
                v.message,
              ].join(' ').toLowerCase())
          .toList();
      // Invalidate downstream caches whenever the items identity flips
      // (refresh, cluster switch).
      _cachedFiltered = null;
      _cachedNamespaces = null;
    }

    // Reuse the previous result when nothing relevant changed —
    // RefreshIndicator + Riverpod rebuilds re-enter build() many times
    // per second on a single scroll frame; at 1000 violations the
    // unfiltered 3000-predicate loop adds up quickly.
    if (_cachedFiltered != null &&
        _cachedFilterNamespace == widget.namespaceFilter &&
        _cachedFilterSeverity == widget.severityFilter &&
        _cachedFilterSearch == q) {
      return _cachedFiltered!;
    }

    final hs = _haystacks!;
    final out = <PolicyViolation>[];
    for (var i = 0; i < items.length; i++) {
      final v = items[i];
      if (widget.namespaceFilter.isNotEmpty &&
          v.namespace != widget.namespaceFilter) {
        continue;
      }
      if (!_matchesSeverity(v)) continue;
      if (q.isNotEmpty && !hs[i].contains(q)) continue;
      out.add(v);
    }

    _cachedFiltered = out;
    _cachedFilterNamespace = widget.namespaceFilter;
    _cachedFilterSeverity = widget.severityFilter;
    _cachedFilterSearch = q;
    return out;
  }

  bool _matchesSeverity(PolicyViolation v) => switch (widget.severityFilter) {
        PolicySeverityFilter.all => true,
        PolicySeverityFilter.critical => v.severity == 'critical',
        PolicySeverityFilter.high => v.severity == 'high',
        PolicySeverityFilter.medium => v.severity == 'medium',
        PolicySeverityFilter.low => v.severity == 'low',
      };
}

// ---------------------------------------------------------------------------
// Filter strip
// ---------------------------------------------------------------------------

class _FilterStrip extends StatelessWidget {
  const _FilterStrip({
    required this.namespaceFilter,
    required this.namespaces,
    required this.severityFilter,
    required this.searchCtl,
    required this.totalVisible,
    required this.totalAll,
    required this.onNamespaceChanged,
    required this.onSeverityChanged,
    required this.onSearchChanged,
  });

  final String namespaceFilter;
  final List<String> namespaces;
  final PolicySeverityFilter severityFilter;
  final TextEditingController searchCtl;
  final int totalVisible;
  final int totalAll;
  final ValueChanged<String> onNamespaceChanged;
  final ValueChanged<PolicySeverityFilter> onSeverityChanged;
  final ValueChanged<String> onSearchChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    // Guard against a stale namespace filter that's no longer in the
    // current data set (e.g., cluster switch landed mid-build before
    // ref.listen has fired). Dropdown raises on missing value otherwise.
    final dropdownValue = namespaceFilter.isEmpty ||
            namespaces.contains(namespaceFilter)
        ? namespaceFilter
        : '';
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
              hintText: 'Search violations',
              isDense: true,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(6),
              ),
            ),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: DropdownButtonFormField<String>(
                  initialValue: dropdownValue,
                  isDense: true,
                  decoration: InputDecoration(
                    labelText: 'Namespace',
                    isDense: true,
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(6),
                    ),
                  ),
                  items: [
                    const DropdownMenuItem(value: '', child: Text('All')),
                    for (final ns in namespaces)
                      DropdownMenuItem(value: ns, child: Text(ns)),
                  ],
                  onChanged: (v) => onNamespaceChanged(v ?? ''),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                // Const records aren't allowed when the field is an enum
                // value — dropping the `const` lets the literal stay
                // inline without lifting a private const helper.
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
              ],
            ),
          ),
          const SizedBox(height: 4),
          Text(
            totalVisible == totalAll
                ? '$totalAll ${totalAll == 1 ? 'violation' : 'violations'}'
                : 'Showing $totalVisible of $totalAll violations',
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

class _ViolationRow extends StatelessWidget {
  const _ViolationRow({required this.violation, required this.onTap});

  final PolicyViolation violation;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final target = '${violation.kind}/${violation.name}';
    final scope = violation.namespace.isEmpty
        ? 'cluster-scoped'
        : violation.namespace;

    return InkWell(
      onTap: onTap,
      child: Container(
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
                    violation.policy,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (violation.rule.isNotEmpty)
                    Text(
                      'rule: ${violation.rule}',
                      style:
                          TextStyle(color: colors.textSecondary, fontSize: 11),
                      overflow: TextOverflow.ellipsis,
                    ),
                  const SizedBox(height: 2),
                  Text(
                    '$target · $scope',
                    style: TextStyle(color: colors.textSecondary, fontSize: 12),
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (violation.message.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 4),
                      child: Text(
                        violation.message,
                        style: TextStyle(color: colors.textMuted, fontSize: 11),
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  const SizedBox(height: 4),
                  Wrap(
                    spacing: 6,
                    runSpacing: 4,
                    children: [
                      EngineBadge(engine: violation.engine, dense: true),
                      SeverityBadge(severity: violation.severity, dense: true),
                      BlockingBadge(blocking: violation.blocking, dense: true),
                    ],
                  ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            ExcludeSemantics(
              child: Icon(Icons.chevron_right, size: 16, color: colors.textMuted),
            ),
          ],
        ),
      ),
    );
  }
}
