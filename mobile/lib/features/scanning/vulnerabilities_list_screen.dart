// Vulnerability list — per-namespace workload-level CVE counts. Web
// parallel: the lower half of `frontend/islands/VulnerabilityDashboard.tsx`.
//
// Behaviour:
//   * Mandatory namespace picker on first visit (matches the backend's
//     hard 400 on missing `?namespace=` param). The picker is a bottom
//     sheet showing the namespaces the active user can list — fetched
//     via `resourceListProvider(kind: 'namespaces')`.
//   * Severity filter chips (`Any / Critical / High / Medium / Low /
//     None`). `None` filters to workloads with zero CVEs at any
//     severity — confirms scan coverage on clean images.
//   * Scanner filter chips render only when both Trivy + Kubescape
//     report rows for the current namespace (otherwise the discrimina-
//     tor is implicit).
//   * Free-text search across workload name + kind + image refs.
//   * Virtual scroll via `SliverChildBuilderDelegate` so 5000+-row
//     responses don't drop frames.
//
// Tapping a row routes to the detail screen. Kubescape rows tap
// through, but the detail screen surfaces a 501 with help copy
// pointing the operator at Trivy — backend doesn't expose per-CVE
// detail for Kubescape's `VulnerabilitySummary` CRD.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/resource_repository.dart';
import '../../api/scanning_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/refresh_guard.dart';
import 'scanning_widgets.dart';

class VulnerabilitiesListScreen extends ConsumerStatefulWidget {
  const VulnerabilitiesListScreen({super.key, this.initialNamespace});

  /// Seed the namespace filter on mount; deep links from a workload's
  /// detail screen pass a value. Drawer entries leave this null and
  /// trigger the bottom-sheet picker on first visit.
  final String? initialNamespace;

  @override
  ConsumerState<VulnerabilitiesListScreen> createState() =>
      _VulnerabilitiesListScreenState();
}

class _VulnerabilitiesListScreenState
    extends ConsumerState<VulnerabilitiesListScreen> {
  String? _namespace;
  SeverityFilter _severityFilter = SeverityFilter.all;
  Scanner? _scannerFilter; // null = show both
  String _search = '';
  final _searchCtl = TextEditingController();
  bool _promptShown = false;

  @override
  void initState() {
    super.initState();
    _namespace = widget.initialNamespace;
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
        // Cluster switch invalidates the namespace selection — every
        // cluster has its own namespace set. Reset filters too so the
        // operator doesn't see stale state.
        setState(() {
          _namespace = null;
          _severityFilter = SeverityFilter.all;
          _scannerFilter = null;
          _search = '';
          _searchCtl.clear();
          _promptShown = false;
        });
      }
    });

    return Scaffold(
      appBar: AppBar(
        title: const Text('Vulnerabilities'),
        actions: [
          if (_namespace != null)
            IconButton(
              icon: const Icon(Icons.swap_horiz),
              tooltip: 'Change namespace',
              onPressed: () => _openNamespacePicker(),
            ),
        ],
      ),
      body: ScanningStatusGate(
        builder: (clusterId, status) {
          // First-time visit + no deep-linked namespace → prompt the
          // bottom sheet once the build settles. The `_promptShown`
          // guard prevents repeat prompts when setState fires for
          // unrelated reasons (severity chip tap, search change).
          if (_namespace == null && !_promptShown) {
            _promptShown = true;
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (mounted) _openNamespacePicker();
            });
          }
          if (_namespace == null) {
            return _NamespacePromptScreen(
              onPick: () => _openNamespacePicker(),
            );
          }
          return _ListBody(
            clusterId: clusterId,
            namespace: _namespace!,
            status: status,
            severityFilter: _severityFilter,
            scannerFilter: _scannerFilter,
            search: _search,
            searchCtl: _searchCtl,
            onSeverityChanged: (v) => setState(() => _severityFilter = v),
            onScannerChanged: (v) => setState(() => _scannerFilter = v),
            onSearchChanged: (v) => setState(() => _search = v),
          );
        },
      ),
    );
  }

  Future<void> _openNamespacePicker() async {
    final clusterId = ref.read(activeClusterProvider);
    final picked = await showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      builder: (context) => _NamespacePickerSheet(clusterId: clusterId),
    );
    if (!mounted) return;
    if (picked != null && picked.isNotEmpty) {
      setState(() => _namespace = picked);
    }
  }
}

// ---------------------------------------------------------------------------
// List body
// ---------------------------------------------------------------------------

class _ListBody extends ConsumerStatefulWidget {
  const _ListBody({
    required this.clusterId,
    required this.namespace,
    required this.status,
    required this.severityFilter,
    required this.scannerFilter,
    required this.search,
    required this.searchCtl,
    required this.onSeverityChanged,
    required this.onScannerChanged,
    required this.onSearchChanged,
  });

  final String clusterId;
  final String namespace;
  final ScanningStatus status;
  final SeverityFilter severityFilter;
  final Scanner? scannerFilter;
  final String search;
  final TextEditingController searchCtl;
  final ValueChanged<SeverityFilter> onSeverityChanged;
  final ValueChanged<Scanner?> onScannerChanged;
  final ValueChanged<String> onSearchChanged;

  @override
  ConsumerState<_ListBody> createState() => _ListBodyState();
}

class _ListBodyState extends ConsumerState<_ListBody> with RefreshGuardMixin {
  // Cached filtered list — `RefreshIndicator` + Riverpod rebuilds re-enter
  // build() many times per scroll frame; at 5000 workloads the unfiltered
  // 4-predicate loop adds up quickly. Invalidated whenever any cache-key
  // field changes (items identity / namespace / severity / scanner /
  // search).
  List<WorkloadVulnSummary>? _lastItems;
  List<String>? _haystacks;
  List<WorkloadVulnSummary>? _cachedFiltered;
  SeverityFilter? _cachedFilterSeverity;
  Scanner? _cachedFilterScanner;
  String? _cachedFilterSearch;

  VulnListKey get _key =>
      VulnListKey(clusterId: widget.clusterId, namespace: widget.namespace);

  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(vulnerabilitiesListProvider(_key));
        try {
          await ref.read(vulnerabilitiesListProvider(_key).future);
        } on Object {
          // surfaces via .when
        }
      });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(vulnerabilitiesListProvider(_key));

    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ListErrorShell(
          title: 'Failed to load vulnerabilities',
          error: e,
          onRetry: _handleRefresh,
        ),
        data: (resp) {
          final filtered = _applyFilters(resp.vulnerabilities);
          // Show the scanner discriminator chip row only when both
          // scanners actually contributed rows in this response. With a
          // single scanner the chips would be a no-op widget that
          // wastes vertical real estate.
          final showScannerChips = resp.vulnerabilities.any(
                (w) => w.scanner == Scanner.trivy,
              ) &&
              resp.vulnerabilities.any(
                (w) => w.scanner == Scanner.kubescape,
              );
          final stale = resp.vulnerabilities.any(
            (w) => isScanStale(w.lastScanned),
          );
          return CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: _FilterStrip(
                  namespace: widget.namespace,
                  searchCtl: widget.searchCtl,
                  severityFilter: widget.severityFilter,
                  scannerFilter: widget.scannerFilter,
                  showScannerChips: showScannerChips,
                  summary: resp.summary,
                  totalVisible: filtered.length,
                  totalAll: resp.vulnerabilities.length,
                  onSeverityChanged: widget.onSeverityChanged,
                  onScannerChanged: widget.onScannerChanged,
                  onSearchChanged: widget.onSearchChanged,
                ),
              ),
              if (stale)
                SliverToBoxAdapter(
                  child: StaleScanBanner(
                    lastScannedIso: resp.vulnerabilities
                        .map((w) => w.lastScanned)
                        .firstWhere(
                          (s) => s.isNotEmpty,
                          orElse: () => '',
                        ),
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
                        resp.vulnerabilities.isEmpty
                            ? 'No vulnerability reports in this namespace. '
                                'The scanner may not have scanned the '
                                'workloads here yet.'
                            : 'No workloads match the current filters.',
                        textAlign: TextAlign.center,
                        style: TextStyle(color: colors.textMuted),
                      ),
                    ),
                  ),
                )
              else
                SliverList(
                  // Virtual scroll via SliverChildBuilderDelegate handles
                  // 5000+-workload responses without dropping frames —
                  // only the visible rows are constructed.
                  delegate: SliverChildBuilderDelegate(
                    (context, index) => _WorkloadRow(
                      workload: filtered[index],
                      onTap: () => context.push(
                        '/clusters/${widget.clusterId}/scanning/'
                        'vulnerabilities/'
                        '${Uri.encodeComponent(filtered[index].namespace)}/'
                        '${Uri.encodeComponent(filtered[index].kind)}/'
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

  List<WorkloadVulnSummary> _applyFilters(List<WorkloadVulnSummary> items) {
    final q = widget.search.trim().toLowerCase();

    if (!identical(_lastItems, items)) {
      _lastItems = items;
      _haystacks = items.map((w) {
        final imgs = w.images.map((i) => i.image).join(' ');
        return '${w.namespace} ${w.kind} ${w.name} $imgs'.toLowerCase();
      }).toList();
      _cachedFiltered = null;
    }

    if (_cachedFiltered != null &&
        _cachedFilterSeverity == widget.severityFilter &&
        _cachedFilterScanner == widget.scannerFilter &&
        _cachedFilterSearch == q) {
      return _cachedFiltered!;
    }

    final hs = _haystacks!;
    final out = <WorkloadVulnSummary>[];
    for (var i = 0; i < items.length; i++) {
      final w = items[i];
      if (widget.scannerFilter != null && w.scanner != widget.scannerFilter) {
        continue;
      }
      if (!_matchesSeverity(w)) continue;
      if (q.isNotEmpty && !hs[i].contains(q)) continue;
      out.add(w);
    }

    // Sort by critical+high descending — same comparator as the web's
    // `frontend/islands/VulnerabilityDashboard.tsx`. Tiebreaker on name
    // for deterministic test fixtures.
    out.sort((a, b) {
      final c = b.total.severityScore.compareTo(a.total.severityScore);
      if (c != 0) return c;
      return a.name.compareTo(b.name);
    });

    _cachedFiltered = out;
    _cachedFilterSeverity = widget.severityFilter;
    _cachedFilterScanner = widget.scannerFilter;
    _cachedFilterSearch = q;
    return out;
  }

  bool _matchesSeverity(WorkloadVulnSummary w) =>
      switch (widget.severityFilter) {
        SeverityFilter.all => true,
        SeverityFilter.critical => w.total.critical > 0,
        SeverityFilter.high => w.total.high > 0,
        SeverityFilter.medium => w.total.medium > 0,
        SeverityFilter.low => w.total.low > 0,
        SeverityFilter.none => w.total.total == 0,
      };
}

// ---------------------------------------------------------------------------
// Filter strip
// ---------------------------------------------------------------------------

class _FilterStrip extends StatelessWidget {
  const _FilterStrip({
    required this.namespace,
    required this.searchCtl,
    required this.severityFilter,
    required this.scannerFilter,
    required this.showScannerChips,
    required this.summary,
    required this.totalVisible,
    required this.totalAll,
    required this.onSeverityChanged,
    required this.onScannerChanged,
    required this.onSearchChanged,
  });

  final String namespace;
  final TextEditingController searchCtl;
  final SeverityFilter severityFilter;
  final Scanner? scannerFilter;
  final bool showScannerChips;
  final VulnListMetadata summary;
  final int totalVisible;
  final int totalAll;
  final ValueChanged<SeverityFilter> onSeverityChanged;
  final ValueChanged<Scanner?> onScannerChanged;
  final ValueChanged<String> onSearchChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.folder_outlined, color: colors.textMuted, size: 16),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  'Namespace: $namespace',
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          // Aggregate severity row — comes from the backend `summary`
          // envelope, so it reflects the full unfiltered count even when
          // chips/search narrow the table.
          Row(
            children: [
              SeverityCountChip(
                label: 'Critical',
                count: summary.severity.critical,
                severity: 'critical',
                onTap: () => onSeverityChanged(
                  severityFilter == SeverityFilter.critical
                      ? SeverityFilter.all
                      : SeverityFilter.critical,
                ),
                selected: severityFilter == SeverityFilter.critical,
              ),
              const SizedBox(width: 6),
              SeverityCountChip(
                label: 'High',
                count: summary.severity.high,
                severity: 'high',
                onTap: () => onSeverityChanged(
                  severityFilter == SeverityFilter.high
                      ? SeverityFilter.all
                      : SeverityFilter.high,
                ),
                selected: severityFilter == SeverityFilter.high,
              ),
              const SizedBox(width: 6),
              SeverityCountChip(
                label: 'Med',
                count: summary.severity.medium,
                severity: 'medium',
                onTap: () => onSeverityChanged(
                  severityFilter == SeverityFilter.medium
                      ? SeverityFilter.all
                      : SeverityFilter.medium,
                ),
                selected: severityFilter == SeverityFilter.medium,
              ),
              const SizedBox(width: 6),
              SeverityCountChip(
                label: 'Low',
                count: summary.severity.low,
                severity: 'low',
                onTap: () => onSeverityChanged(
                  severityFilter == SeverityFilter.low
                      ? SeverityFilter.all
                      : SeverityFilter.low,
                ),
                selected: severityFilter == SeverityFilter.low,
              ),
            ],
          ),
          const SizedBox(height: 8),
          TextField(
            controller: searchCtl,
            onChanged: onSearchChanged,
            decoration: InputDecoration(
              prefixIcon: const Icon(Icons.search, size: 18),
              hintText: 'Search workloads or image refs',
              isDense: true,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(6),
              ),
            ),
          ),
          const SizedBox(height: 8),
          // "None" chip surfaced as part of the severity row so the
          // operator can confirm clean-image coverage. Renders on the
          // same horizontally scrollable row.
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                for (final tuple in const [
                  (SeverityFilter.all, 'Any severity'),
                  (SeverityFilter.none, 'No CVEs'),
                ])
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: ChoiceChip(
                      selected: tuple.$1 == severityFilter,
                      label: Text(tuple.$2),
                      onSelected: (_) => onSeverityChanged(tuple.$1),
                    ),
                  ),
                if (showScannerChips) ...[
                  const SizedBox(width: 6),
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: ChoiceChip(
                      selected: scannerFilter == null,
                      label: const Text('All scanners'),
                      onSelected: (_) => onScannerChanged(null),
                    ),
                  ),
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: ChoiceChip(
                      selected: scannerFilter == Scanner.trivy,
                      label: const Text('Trivy'),
                      onSelected: (_) => onScannerChanged(Scanner.trivy),
                    ),
                  ),
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: ChoiceChip(
                      selected: scannerFilter == Scanner.kubescape,
                      label: const Text('Kubescape'),
                      onSelected: (_) => onScannerChanged(Scanner.kubescape),
                    ),
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(height: 4),
          Text(
            totalVisible == totalAll
                ? '$totalAll ${totalAll == 1 ? 'workload' : 'workloads'}'
                : 'Showing $totalVisible of $totalAll workloads',
            style: TextStyle(color: colors.textMuted, fontSize: 11),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Workload row
// ---------------------------------------------------------------------------

class _WorkloadRow extends StatelessWidget {
  const _WorkloadRow({required this.workload, required this.onTap});

  final WorkloadVulnSummary workload;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final t = workload.total;
    final imageHint = workload.images.length == 1
        ? workload.images.first.image
        : workload.images.isEmpty
            ? 'No images'
            : '${workload.images.length} images';
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          border: Border(bottom: BorderSide(color: colors.borderSubtle)),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    workload.name,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    '${workload.kind} · $imageHint',
                    style: TextStyle(color: colors.textSecondary, fontSize: 12),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 6),
                  Wrap(
                    spacing: 6,
                    runSpacing: 4,
                    children: [
                      if (t.critical > 0)
                        _SeverityChip(label: 'C ${t.critical}', severity: 'critical'),
                      if (t.high > 0)
                        _SeverityChip(label: 'H ${t.high}', severity: 'high'),
                      if (t.medium > 0)
                        _SeverityChip(label: 'M ${t.medium}', severity: 'medium'),
                      if (t.low > 0)
                        _SeverityChip(label: 'L ${t.low}', severity: 'low'),
                      if (t.total == 0)
                        Text(
                          'No CVEs',
                          style: TextStyle(
                            color: colors.success,
                            fontSize: 11,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ScannerBadge(scanner: workload.scanner, dense: true),
                    ],
                  ),
                ],
              ),
            ),
            Icon(Icons.chevron_right, color: colors.textMuted, size: 16),
          ],
        ),
      ),
    );
  }
}

class _SeverityChip extends StatelessWidget {
  const _SeverityChip({required this.label, required this.severity});

  final String label;
  final String severity;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final fg = scanSeverityColor(severity, colors);
    final bg = scanSeverityDim(severity, colors);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(3),
        border: Border.all(color: fg),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: fg,
          fontSize: 10,
          fontWeight: FontWeight.w700,
          fontFeatures: const [FontFeature.tabularFigures()],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Namespace picker bottom sheet
// ---------------------------------------------------------------------------

class _NamespacePickerSheet extends ConsumerStatefulWidget {
  const _NamespacePickerSheet({required this.clusterId});

  final String clusterId;

  @override
  ConsumerState<_NamespacePickerSheet> createState() =>
      _NamespacePickerSheetState();
}

class _NamespacePickerSheetState extends ConsumerState<_NamespacePickerSheet> {
  String _search = '';

  ResourceListKey get _key =>
      ResourceListKey(clusterId: widget.clusterId, kind: 'namespaces');

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(resourceListProvider(_key));
    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(
          left: 16,
          right: 16,
          top: 16,
          bottom: MediaQuery.of(context).viewInsets.bottom + 16,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    'Choose a namespace',
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.close),
                  onPressed: () => Navigator.of(context).pop(),
                  tooltip: 'Close',
                ),
              ],
            ),
            const SizedBox(height: 8),
            TextField(
              onChanged: (v) => setState(() => _search = v),
              decoration: InputDecoration(
                prefixIcon: const Icon(Icons.search, size: 18),
                hintText: 'Filter namespaces',
                isDense: true,
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(6),
                ),
              ),
            ),
            const SizedBox(height: 12),
            ConstrainedBox(
              constraints: BoxConstraints(
                maxHeight: MediaQuery.of(context).size.height * 0.5,
              ),
              child: async.when(
                loading: () => const Padding(
                  padding: EdgeInsets.all(24),
                  child: Center(child: CircularProgressIndicator()),
                ),
                error: (e, _) => Padding(
                  padding: const EdgeInsets.all(16),
                  child: ErrorStateView(
                    message: 'Unable to load namespaces',
                    onRetry: () => ref.invalidate(resourceListProvider(_key)),
                  ),
                ),
                data: (list) {
                  final q = _search.trim().toLowerCase();
                  final names = <String>{};
                  for (final item in list.items) {
                    final meta = item['metadata'];
                    if (meta is Map) {
                      final n = meta['name'];
                      if (n is String && n.isNotEmpty) names.add(n);
                    }
                  }
                  final filtered = names
                      .where((n) => q.isEmpty || n.toLowerCase().contains(q))
                      .toList()
                    ..sort();
                  if (filtered.isEmpty) {
                    return Padding(
                      padding: const EdgeInsets.all(24),
                      child: Text(
                        names.isEmpty
                            ? 'No namespaces are visible to your account.'
                            : 'No namespaces match the search.',
                        textAlign: TextAlign.center,
                        style: TextStyle(color: colors.textMuted),
                      ),
                    );
                  }
                  return ListView.builder(
                    shrinkWrap: true,
                    itemCount: filtered.length,
                    itemBuilder: (context, i) {
                      final n = filtered[i];
                      return InkWell(
                        onTap: () => Navigator.of(context).pop(n),
                        child: Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 4,
                            vertical: 12,
                          ),
                          decoration: BoxDecoration(
                            border: Border(
                              bottom: BorderSide(color: colors.borderSubtle),
                            ),
                          ),
                          child: Row(
                            children: [
                              Icon(
                                Icons.folder_outlined,
                                color: colors.textMuted,
                                size: 18,
                              ),
                              const SizedBox(width: 8),
                              Expanded(
                                child: Text(
                                  n,
                                  style: TextStyle(
                                    color: colors.textPrimary,
                                    fontSize: 14,
                                  ),
                                ),
                              ),
                              Icon(
                                Icons.chevron_right,
                                color: colors.textMuted,
                                size: 16,
                              ),
                            ],
                          ),
                        ),
                      );
                    },
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Pre-pick empty state
// ---------------------------------------------------------------------------

class _NamespacePromptScreen extends StatelessWidget {
  const _NamespacePromptScreen({required this.onPick});

  final VoidCallback onPick;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.folder_open, color: colors.textMuted, size: 40),
            const SizedBox(height: 12),
            Text(
              'Choose a namespace',
              style: TextStyle(
                color: colors.textPrimary,
                fontSize: 16,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 6),
            Text(
              'Vulnerability reports are scoped per namespace to keep the '
              'response size manageable. Pick one to view workload CVEs.',
              textAlign: TextAlign.center,
              style: TextStyle(color: colors.textMuted, fontSize: 13),
            ),
            const SizedBox(height: 16),
            FilledButton.icon(
              onPressed: onPick,
              icon: const Icon(Icons.folder_outlined, size: 18),
              label: const Text('Pick namespace'),
            ),
          ],
        ),
      ),
    );
  }
}
