// Shared pill + small-widget helpers consumed by every scanning surface
// (dashboard, vulnerabilities list, detail). Keeping the severity /
// scanner / fix-availability pills in one file is what enforces R10
// (web/dart isomorphism) for the scanning domain — a future regression
// where `critical` rendered as `warning` instead of `error` would have
// to land here first.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/scanning_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/feature_unavailable_state.dart';

/// Wraps a per-cluster scanning surface body in the standard
/// discovery-status gate. The outer ConsumerWidget watches
/// `scanningStatusProvider(clusterId)`, handles loading / error /
/// not-detected uniformly, and only calls [builder] when at least one
/// scanner is detected. Mirrors `PolicyStatusGate` from PR-4i.
class ScanningStatusGate extends ConsumerWidget {
  const ScanningStatusGate({super.key, required this.builder});

  /// Called with the current activeClusterId and the resolved
  /// [ScanningStatus] once status reports `detected: true`. Both pieces
  /// flow through because two consumers need them: the dashboard
  /// renders per-scanner install state on cards, and the list screen
  /// needs the scanner availability to decide which discriminator chips
  /// to show.
  final Widget Function(String clusterId, ScanningStatus status) builder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(scanningStatusProvider(clusterId));
    return statusAsync.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => ErrorStateView(
        message: e is ApiError ? e.message : e.toString(),
        onRetry: () => ref.invalidate(scanningStatusProvider(clusterId)),
      ),
      data: (status) {
        // Transient backend 5xx — the repository collapses 5xx to
        // `unreachable` with `serviceUnavailable: true`. Distinct from
        // "scanner not installed": the operator needs a retry path
        // (the backend will return when the rolling restart settles),
        // not install-guidance copy.
        if (status.serviceUnavailable) {
          return ErrorStateView(
            message:
                'Scanning backend temporarily unavailable. Retry once the '
                'cluster settles.',
            onRetry: () => ref.invalidate(scanningStatusProvider(clusterId)),
          );
        }
        if (!status.detected) return FeatureUnavailableState.scanning();
        return builder(clusterId, status);
      },
    );
  }
}

/// Severity → KubeColors mapping. The only place severity colours live
/// across all scanning surfaces — chips, badges, severity counts and
/// the CVE row severity column all read from here. `unknown` reads as
/// muted text so it never carries error-grade red weight.
///
/// Web parallel: `frontend/components/ui/ScanBadges.tsx::SEVERITY_COLORS`.
Color scanSeverityColor(String severity, KubeColors colors) {
  switch (severity.toLowerCase()) {
    case 'critical':
      return colors.error;
    case 'high':
      return colors.warning;
    case 'medium':
      return colors.accent;
    case 'low':
      return colors.success;
    default:
      // `unknown` + anything else falls into the muted bucket.
      return colors.textMuted;
  }
}

/// Background tint paired with [scanSeverityColor]. Used by chip
/// renderers that need a fill distinct from the text colour.
Color scanSeverityDim(String severity, KubeColors colors) {
  switch (severity.toLowerCase()) {
    case 'critical':
      return colors.errorDim;
    case 'high':
      return colors.warningDim;
    case 'medium':
      return colors.accentDim;
    case 'low':
      return colors.successDim;
    default:
      return colors.bgSurface;
  }
}

/// Human-friendly label for a severity bucket. Capitalises so
/// `critical` reads as `Critical` — matches the web's `<SeverityBadge>`.
String scanSeverityLabel(String severity) {
  if (severity.isEmpty) return 'Unknown';
  return severity.substring(0, 1).toUpperCase() + severity.substring(1);
}

/// Severity-count pill — the four-up `Critical / High / Medium / Low`
/// row at the top of the dashboard + detail screens. Renders a muted
/// pill when the count is zero so the absence of a severity is still
/// visible.
class SeverityCountChip extends StatelessWidget {
  const SeverityCountChip({
    super.key,
    required this.label,
    required this.count,
    required this.severity,
    this.onTap,
    this.selected = false,
  });

  final String label;
  final int count;
  final String severity;
  final VoidCallback? onTap;
  final bool selected;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final fg = count > 0 ? scanSeverityColor(severity, colors) : colors.textMuted;
    final pill = Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
      decoration: BoxDecoration(
        color: scanSeverityDim(severity, colors),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(
          color: selected ? fg : Colors.transparent,
          width: selected ? 1.5 : 1,
        ),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '$count',
            style: TextStyle(
              color: fg,
              fontSize: 13,
              fontWeight: FontWeight.w700,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
          const SizedBox(width: 4),
          Text(
            label,
            style: TextStyle(
              color: fg,
              fontSize: 11,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
    if (onTap == null) return pill;
    return InkWell(onTap: onTap, borderRadius: BorderRadius.circular(4), child: pill);
  }
}

/// CVE severity badge — used inside the detail-screen CVE table and as
/// the severity chip on workload list rows.
///
/// [label] overrides the default `scanSeverityLabel(severity)` text —
/// pass a short form like `'C 5'` for compact count chips in the list.
/// When [label] is non-null the text renders with tabular figures and
/// w700 weight for numeric readability.
class CVESeverityBadge extends StatelessWidget {
  const CVESeverityBadge({super.key, required this.severity, this.label});

  final String severity;

  /// Optional override for the display text. When non-null, [FontWeight.w700]
  /// and [FontFeature.tabularFigures] are applied for numeric legibility.
  final String? label;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final fg = scanSeverityColor(severity, colors);
    final bg = scanSeverityDim(severity, colors);
    final displayLabel = label ?? scanSeverityLabel(severity);
    final isCount = label != null;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(3),
        border: Border.all(color: fg),
      ),
      child: Text(
        displayLabel,
        style: TextStyle(
          color: fg,
          fontSize: 10,
          fontWeight: isCount ? FontWeight.w700 : FontWeight.w600,
          fontFeatures: isCount ? const [FontFeature.tabularFigures()] : null,
        ),
      ),
    );
  }
}

/// "Fix available" / "No fix" pill. Mirrors `<FixAvailableBadge>`.
class FixAvailableBadge extends StatelessWidget {
  const FixAvailableBadge({super.key, required this.fixedVersion});

  final String fixedVersion;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final hasFix = fixedVersion.isNotEmpty;
    final fg = hasFix ? colors.success : colors.textMuted;
    final bg = hasFix ? colors.successDim : colors.bgSurface;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(3),
        border: Border.all(color: fg),
      ),
      child: Text(
        hasFix ? fixedVersion : 'No fix',
        style: TextStyle(
          color: fg,
          fontSize: 10,
          fontFamily: hasFix ? 'monospace' : null,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

/// Scanner pill — distinct color per scanner for visual scan-ability
/// across mixed-engine clusters. Mirrors `<ScannerBadge>`.
class ScannerBadge extends StatelessWidget {
  const ScannerBadge({super.key, required this.scanner, this.dense = false});

  final Scanner scanner;
  final bool dense;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final (fg, bg) = switch (scanner) {
      Scanner.trivy => (colors.info, colors.bgSurface),
      Scanner.kubescape => (colors.accent, colors.accentDim),
      Scanner.unknown => (colors.textMuted, colors.bgSurface),
    };
    return Container(
      padding: EdgeInsets.symmetric(
        horizontal: dense ? 6 : 8,
        vertical: dense ? 2 : 3,
      ),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(dense ? 3 : 4),
        border: Border.all(color: fg),
      ),
      child: Text(
        scannerLabel(scanner),
        style: TextStyle(
          color: fg,
          fontSize: dense ? 10 : 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

/// Stale-scan banner. Renders when the latest scan timestamp is older
/// than [kScanStaleThreshold]; consumers should call [isScanStale]
/// before mounting this widget so the banner stays out of the tree
/// when not needed (rather than render-then-hide).
class StaleScanBanner extends StatelessWidget {
  const StaleScanBanner({super.key, required this.lastScannedIso});

  final String lastScannedIso;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: colors.warningDim,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: colors.warning),
      ),
      child: Row(
        children: [
          Icon(Icons.schedule, color: colors.warning, size: 18),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Latest scan is more than ${kScanStaleThreshold.inDays} days old. '
              'Findings may not reflect recently-deployed images.',
              style: TextStyle(color: colors.textPrimary, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}
