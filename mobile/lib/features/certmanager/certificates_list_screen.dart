// Certificates list — every cert-manager Certificate the active user
// can read across all namespaces. Filter chips (All / Expiring / Failed),
// search by name / namespace / issuer, plus a `?status=expiring` URL
// param that seeds the chip to "Expiring" on mount. Mirrors the web's
// `frontend/islands/CertificatesList.tsx` filter strategy.
//
// Expiry badge color is computed from each cert's resolved
// `warningThresholdDays` / `criticalThresholdDays` pair — the backend
// resolver supplies these per the annotation chain (cert → issuer →
// clusterissuer → package default), so the mobile UI never hardcodes
// thresholds. When the resolver hasn't run (older response shape) the
// badge falls back to the package defaults (warn 30, crit 7) so colors
// still match operator expectations.
//
// Status gating: `certManagerStatusProvider` gates the surface — when
// cert-manager is not detected the operator sees
// `FeatureUnavailableState.certManager()` rather than an empty list.
//
// Tap row → `/clusters/<id>/certificates/certificates/<namespace>/<name>`.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/api_error.dart';
import '../../api/certmanager_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/feature_unavailable_state.dart';
import 'cert_badges.dart';

/// Tri-state filter chip on the list screen.
enum CertStatusFilter { all, expiring, failed }

class CertificatesListScreen extends ConsumerStatefulWidget {
  const CertificatesListScreen({super.key, this.initialStatusFilter});

  /// Seed the status chip on mount. Drawer deep-links don't seed this;
  /// the dashboard "Expiring" tile + the expiry-notification push deep
  /// link do (`?status=expiring`).
  final String? initialStatusFilter;

  @override
  ConsumerState<CertificatesListScreen> createState() =>
      _CertificatesListScreenState();
}

class _CertificatesListScreenState
    extends ConsumerState<CertificatesListScreen> {
  late CertStatusFilter _filter;
  String _search = '';
  final _searchCtl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _filter = switch (widget.initialStatusFilter) {
      'expiring' => CertStatusFilter.expiring,
      'failed' => CertStatusFilter.failed,
      _ => CertStatusFilter.all,
    };
  }

  @override
  void dispose() {
    _searchCtl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(certManagerStatusProvider(clusterId));
    // Reset filter + search when the user switches clusters so stale
    // state from the previous cluster doesn't leak through. The list
    // provider's family key already invalidates on clusterId change;
    // this clears the UI inputs themselves.
    ref.listen<String>(activeClusterProvider, (previous, next) {
      if (previous != next) {
        setState(() {
          _filter = CertStatusFilter.all;
          _search = '';
          _searchCtl.clear();
        });
      }
    });

    return Scaffold(
      appBar: AppBar(title: const Text('Certificates')),
      body: statusAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(e.toString()),
          ),
        ),
        data: (status) {
          if (!status.detected) return FeatureUnavailableState.certManager();
          return _CertListBody(
            clusterId: clusterId,
            filter: _filter,
            search: _search,
            searchCtl: _searchCtl,
            onFilterChanged: (v) => setState(() => _filter = v),
            onSearchChanged: (v) => setState(() => _search = v),
          );
        },
      ),
    );
  }
}

class _CertListBody extends ConsumerWidget {
  const _CertListBody({
    required this.clusterId,
    required this.filter,
    required this.search,
    required this.searchCtl,
    required this.onFilterChanged,
    required this.onSearchChanged,
  });

  final String clusterId;
  final CertStatusFilter filter;
  final String search;
  final TextEditingController searchCtl;
  final ValueChanged<CertStatusFilter> onFilterChanged;
  final ValueChanged<String> onSearchChanged;

  CertificateListKey get _key => CertificateListKey(clusterId: clusterId);

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(certificateListProvider(_key));

    Future<void> handleRefresh() async {
      ref.invalidate(certificateListProvider(_key));
      try {
        await ref.read(certificateListProvider(_key).future);
      } on Object {
        // surfaces via .when error branch
      }
    }

    return RefreshIndicator(
      onRefresh: handleRefresh,
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _errorShell(e, handleRefresh, colors),
        data: (certs) {
          final filtered = _applyFilters(certs);
          return CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: _FilterStrip(
                  filter: filter,
                  searchCtl: searchCtl,
                  totalVisible: filtered.length,
                  totalAll: certs.length,
                  onFilterChanged: onFilterChanged,
                  onSearchChanged: onSearchChanged,
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
                        certs.isEmpty
                            ? 'No certificates yet. cert-manager will populate '
                                'this list once it issues certificates.'
                            : 'No certificates match the current filters.',
                        style: TextStyle(color: colors.textMuted),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                )
              else
                SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) => _CertRow(
                      cert: filtered[index],
                      onTap: () => context.push(
                        '/clusters/$clusterId/certificates/certificates/'
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

  List<Certificate> _applyFilters(List<Certificate> certs) {
    final q = search.trim().toLowerCase();
    return certs.where((c) {
      switch (filter) {
        case CertStatusFilter.expiring:
          if (c.status != CertStatus.expiring &&
              c.status != CertStatus.expired) {
            return false;
          }
          break;
        case CertStatusFilter.failed:
          if (c.status != CertStatus.failed) return false;
          break;
        case CertStatusFilter.all:
          break;
      }
      if (q.isEmpty) return true;
      return c.name.toLowerCase().contains(q) ||
          c.namespace.toLowerCase().contains(q) ||
          c.issuerRef.name.toLowerCase().contains(q);
    }).toList();
  }

  Widget _errorShell(Object e, Future<void> Function() retry, KubeColors c) {
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [
        SizedBox(
          height: 280,
          child: Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    'Failed to load certificates',
                    style: TextStyle(
                      color: c.textPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    e is ApiError ? e.message : e.toString(),
                    style: TextStyle(color: c.textMuted),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 12),
                  OutlinedButton(
                    onPressed: retry,
                    child: const Text('Retry'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ],
    );
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

  final CertStatusFilter filter;
  final TextEditingController searchCtl;
  final int totalVisible;
  final int totalAll;
  final ValueChanged<CertStatusFilter> onFilterChanged;
  final ValueChanged<String> onSearchChanged;

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
              for (final value in CertStatusFilter.values)
                ChoiceChip(
                  label: Text(switch (value) {
                    CertStatusFilter.all => 'All',
                    CertStatusFilter.expiring => 'Expiring',
                    CertStatusFilter.failed => 'Failed',
                  }),
                  selected: value == filter,
                  onSelected: (_) => onFilterChanged(value),
                ),
            ],
          ),
          const SizedBox(height: 8),
          TextField(
            controller: searchCtl,
            decoration: InputDecoration(
              hintText: 'Filter by name, namespace, issuer…',
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
              '$totalVisible of $totalAll certificates',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}

class _CertRow extends StatelessWidget {
  const _CertRow({required this.cert, required this.onTap});

  final Certificate cert;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final dns = cert.dnsNames.join(', ');
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
                CertStatusPill(status: cert.status),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              '${cert.namespace} · ${cert.issuerRef.kind}/${cert.issuerRef.name}',
              style: TextStyle(color: colors.textSecondary, fontSize: 12),
              overflow: TextOverflow.ellipsis,
            ),
            if (dns.isNotEmpty) ...[
              const SizedBox(height: 2),
              Text(
                dns,
                style: TextStyle(color: colors.textMuted, fontSize: 11),
                overflow: TextOverflow.ellipsis,
              ),
            ],
            const SizedBox(height: 6),
            Row(
              children: [
                ExpiryBadge(
                  daysRemaining: cert.daysRemaining,
                  warningThresholdDays: cert.warningThresholdDays,
                  criticalThresholdDays: cert.criticalThresholdDays,
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
