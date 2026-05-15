// Vulnerability dashboard — top-level scanning surface. Mirrors
// `frontend/islands/VulnerabilityDashboard.tsx` but collapsed for phone
// size: a scanner-status header card + Trivy / Kubescape per-scanner
// cards + a single browse tile to the namespace-scoped vulnerability
// list. The web's namespace selector + workload table is deferred to
// the dedicated list screen so the dashboard renders cleanly without
// the operator picking a namespace first.
//
// Status gating: [ScanningStatusGate] gates the surface — when neither
// scanner is detected the operator sees
// `FeatureUnavailableState.scanning()` rather than empty cards.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/scanning_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/refresh_guard.dart';
import 'scanning_widgets.dart';

class ScanningDashboardScreen extends StatelessWidget {
  const ScanningDashboardScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Vulnerabilities')),
      body: ScanningStatusGate(
        builder: (clusterId, status) => _DashboardBody(
          clusterId: clusterId,
          status: status,
        ),
      ),
    );
  }
}

class _DashboardBody extends ConsumerStatefulWidget {
  const _DashboardBody({required this.clusterId, required this.status});

  final String clusterId;
  final ScanningStatus status;

  @override
  ConsumerState<_DashboardBody> createState() => _DashboardBodyState();
}

class _DashboardBodyState extends ConsumerState<_DashboardBody>
    with RefreshGuardMixin {
  Future<void> _handleRefresh() => guardedRefresh(() async {
        ref.invalidate(scanningStatusProvider(widget.clusterId));
        try {
          await ref.read(scanningStatusProvider(widget.clusterId).future);
        } on Object {
          // ScanningStatusGate handles error rendering on the next build.
        }
      });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final lastChecked = widget.status.lastChecked;
    return RefreshIndicator(
      onRefresh: _handleRefresh,
      child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 16),
        children: [
          _HeaderCard(status: widget.status, colors: colors),
          const SizedBox(height: 12),
          _ScannerCards(status: widget.status, colors: colors),
          const SizedBox(height: 16),
          _BrowseLink(
            clusterId: widget.clusterId,
            colors: colors,
          ),
          if (lastChecked.isNotEmpty) ...[
            const SizedBox(height: 16),
            Center(
              child: Text(
                'Discovery last checked: $lastChecked',
                style: TextStyle(color: colors.textMuted, fontSize: 11),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({required this.status, required this.colors});

  final ScanningStatus status;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final detectedNames = <String>[];
    if (status.trivyAvailable) detectedNames.add('Trivy');
    if (status.kubescapeAvailable) detectedNames.add('Kubescape');
    final summary = detectedNames.isEmpty
        ? 'No scanner detected'
        : '${detectedNames.join(' + ')} detected';
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.security_outlined, color: colors.accent, size: 22),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  summary,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 15,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            'Container image vulnerability reports — pulled from the '
            'scanner CRDs. Reports lag image changes by the scanner\'s '
            'configured interval.',
            style: TextStyle(color: colors.textMuted, fontSize: 12),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Scanner cards (Trivy + Kubescape)
// ---------------------------------------------------------------------------

class _ScannerCards extends StatelessWidget {
  const _ScannerCards({required this.status, required this.colors});

  final ScanningStatus status;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return GridView.count(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      crossAxisCount: 2,
      mainAxisSpacing: 8,
      crossAxisSpacing: 8,
      childAspectRatio: 1.45,
      children: [
        _ScannerCard(
          scanner: Scanner.trivy,
          available: status.trivyAvailable,
          namespace: status.trivy?.namespace,
          colors: colors,
        ),
        _ScannerCard(
          scanner: Scanner.kubescape,
          available: status.kubescapeAvailable,
          namespace: status.kubescape?.namespace,
          colors: colors,
        ),
      ],
    );
  }
}

class _ScannerCard extends StatelessWidget {
  const _ScannerCard({
    required this.scanner,
    required this.available,
    required this.namespace,
    required this.colors,
  });

  final Scanner scanner;
  final bool available;
  final String? namespace;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              ScannerBadge(scanner: scanner, dense: true),
              const Spacer(),
              Icon(
                available ? Icons.check_circle_outline : Icons.cancel_outlined,
                size: 16,
                color: available ? colors.success : colors.textMuted,
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            available ? 'Installed' : 'Not installed',
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          if (available) ...[
            const SizedBox(height: 2),
            Text(
              scanner == Scanner.trivy
                  ? 'CVE-level detail available'
                  : 'Workload summaries only',
              style: TextStyle(color: colors.textSecondary, fontSize: 11),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            if (namespace != null)
              Text(
                'Installed in $namespace',
                style: TextStyle(color: colors.textMuted, fontSize: 11),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
          ] else
            Text(
              scanner == Scanner.trivy
                  ? 'Install Trivy Operator for image CVE scans'
                  : 'Install Kubescape for control compliance scans',
              style: TextStyle(color: colors.textMuted, fontSize: 11),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Browse tile
// ---------------------------------------------------------------------------

class _BrowseLink extends StatelessWidget {
  const _BrowseLink({required this.clusterId, required this.colors});

  final String clusterId;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => context.push('/clusters/$clusterId/scanning/vulnerabilities'),
      child: Container(
        padding: const EdgeInsets.symmetric(vertical: 16, horizontal: 14),
        decoration: BoxDecoration(
          color: colors.bgSurface,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: colors.borderSubtle),
        ),
        child: Row(
          children: [
            Icon(Icons.list_alt_outlined, color: colors.accent, size: 22),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'Browse workload vulnerabilities',
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    'Pick a namespace to view per-workload severity counts.',
                    style:
                        TextStyle(color: colors.textMuted, fontSize: 11),
                  ),
                ],
              ),
            ),
            Icon(Icons.chevron_right, color: colors.textMuted, size: 18),
          ],
        ),
      ),
    );
  }
}
