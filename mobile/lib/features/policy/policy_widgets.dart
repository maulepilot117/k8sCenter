// Shared pill + small-widget helpers consumed by every policy surface
// (dashboard, lists, detail, compliance history). Keeping the engine /
// severity / blocking pills in one file is what enforces R10 (web/dart
// isomorphism) for the policy domain — a future regression where
// `medium` rendered as `error` instead of `warning` would have to land
// here first.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/api_error.dart';
import '../../api/policy_repository.dart';
import '../../cluster/cluster_provider.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import '../../widgets/feature_unavailable_state.dart';

/// Wraps a per-cluster policy surface body in the standard discovery-status
/// gate. The outer ConsumerWidget watches `policyStatusProvider(clusterId)`,
/// handles loading / error / not-detected uniformly, and only calls
/// [builder] when at least one engine is detected. Mirrors `EsoStatusGate`
/// from PR-4h.
class PolicyStatusGate extends ConsumerWidget {
  const PolicyStatusGate({super.key, required this.builder});

  /// Called with the current activeClusterId once status resolves to
  /// `detected: true`. Implementations typically return the screen's
  /// per-cluster body widget. The [PolicyDiscoveryStatus] is exposed
  /// because two consumers need it: the dashboard renders per-engine
  /// install state + webhook counts on the engine cards, and the
  /// policies list applies the engine-availability tooltip via
  /// [PolicyDiscoveryStatus.kyvernoAvailable] / `gatekeeperAvailable`
  /// (PR-3f intersection learning). Consumers that don't need the
  /// status are free to ignore it via the `_` pattern.
  final Widget Function(String clusterId, PolicyDiscoveryStatus status) builder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final clusterId = ref.watch(activeClusterProvider);
    final statusAsync = ref.watch(policyStatusProvider(clusterId));
    return statusAsync.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => ErrorStateView(
        message: e is ApiError ? e.message : e.toString(),
        onRetry: () => ref.invalidate(policyStatusProvider(clusterId)),
      ),
      data: (status) {
        // Transient backend 5xx — the repository collapses 5xx to a
        // detected:false sentinel with serviceUnavailable:true. Distinct
        // from "engine not installed": the operator needs a retry path
        // (the backend will return when the rolling restart settles),
        // not install-guidance copy. Without this branch the dashboard
        // and lists wedge the operator on install-Kyverno copy until
        // they kill the app.
        if (status.serviceUnavailable) {
          return ErrorStateView(
            message:
                'Policy backend temporarily unavailable. Retry once the '
                'cluster settles.',
            onRetry: () => ref.invalidate(policyStatusProvider(clusterId)),
          );
        }
        if (!status.detected) return FeatureUnavailableState.policy();
        return builder(clusterId, status);
      },
    );
  }
}

/// Severity → KubeColors mapping. The mapping is the only place severity
/// colours live across all policy surfaces — chips, badges, severity-
/// breakdown bars, violation cards all read from here.
///
/// Web parallel: `frontend/components/ui/PolicyBadges.tsx::SEVERITY_COLORS`.
Color policySeverityColor(String severity, KubeColors colors) {
  switch (severity.toLowerCase()) {
    case 'critical':
      return colors.error;
    case 'high':
      return colors.warning;
    case 'medium':
      // medium maps to accent in the web (`var(--brand)`); we use accent
      // for the same hue continuity.
      return colors.accent;
    case 'low':
      return colors.success;
    default:
      return colors.textMuted;
  }
}

/// Background tint paired with [policySeverityColor]. Used by chip
/// renderers that need a fill colour distinct from the border / text.
Color policySeverityDim(String severity, KubeColors colors) {
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

/// Human label for a severity string. Capitalises so `critical` reads as
/// `Critical` — UI consistency with the web side's `<SeverityBadge>`.
String policySeverityLabel(String severity) {
  if (severity.isEmpty) return 'Unknown';
  return severity.substring(0, 1).toUpperCase() + severity.substring(1);
}

/// Severity ordering desc (critical first, low last). Used by sort
/// comparators on the policies / violations list when a chip filter
/// changes the displayed subset.
const List<String> kSeverityOrder = ['critical', 'high', 'medium', 'low'];

/// Severity filter chip values shared between the policies list and
/// the violations list. `all` plus the four canonical severities.
enum PolicySeverityFilter { all, critical, high, medium, low }

/// Severity pill. Always renders even for unknown severities so callers
/// don't accidentally drop a row when the engine emits an unfamiliar
/// label.
class SeverityBadge extends StatelessWidget {
  const SeverityBadge({
    super.key,
    required this.severity,
    this.dense = false,
  });

  final String severity;

  /// Dense mode shrinks padding + font for embedding inside list rows
  /// next to the resource name.
  final bool dense;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final fg = policySeverityColor(severity, colors);
    final bg = policySeverityDim(severity, colors);
    final displayLabel = policySeverityLabel(severity);
    return Semantics(
      container: true,
      label: 'Severity: $displayLabel',
      excludeSemantics: true,
      child: Container(
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
          displayLabel,
          style: TextStyle(
            color: fg,
            fontSize: dense ? 10 : 11,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }
}

/// Engine pill. Distinct color per engine for visual scan-ability across
/// mixed-engine clusters. [showUnavailableHint] adds a "Engine not
/// installed" subtitle when the engine isn't detected on this cluster —
/// per PR-3f's engine-intersection learning.
class EngineBadge extends StatelessWidget {
  const EngineBadge({
    super.key,
    required this.engine,
    this.dense = false,
    this.showUnavailableHint = false,
  });

  final PolicyEngine engine;
  final bool dense;
  final bool showUnavailableHint;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    // KubeColors exposes `info` (foreground) without a paired `infoDim` —
    // pair with bgSurface and let the coloured border carry the engine
    // identity. Same compromise used by `EsoStatusPill` for tones outside
    // the success/warning/error/accent set.
    final (fg, bg) = switch (engine) {
      PolicyEngine.kyverno => (colors.info, colors.bgSurface),
      PolicyEngine.gatekeeper => (colors.accent, colors.accentDim),
      PolicyEngine.unknown => (colors.textMuted, colors.bgSurface),
    };
    final engineLabel = policyEngineLabel(engine);
    final pill = Semantics(
      container: true,
      label: 'Policy engine: $engineLabel',
      excludeSemantics: true,
      child: Container(
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
          engineLabel,
          style: TextStyle(
            color: fg,
            fontSize: dense ? 10 : 11,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
    if (showUnavailableHint) {
      return Tooltip(
        message: 'Engine not installed on this cluster',
        child: Opacity(opacity: 0.6, child: pill),
      );
    }
    return pill;
  }
}

/// Blocking / Audit pill. Mirrors the web's `<BlockingBadge>`.
class BlockingBadge extends StatelessWidget {
  const BlockingBadge({
    super.key,
    required this.blocking,
    this.dense = false,
  });

  final bool blocking;
  final bool dense;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final (fg, bg) = blocking
        ? (colors.error, colors.errorDim)
        : (colors.textMuted, colors.bgSurface);
    final modeLabel = blocking ? 'Blocking' : 'Audit';
    return Semantics(
      container: true,
      label: 'Policy mode: $modeLabel',
      excludeSemantics: true,
      child: Container(
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
          modeLabel,
          style: TextStyle(
            color: fg,
            fontSize: dense ? 10 : 11,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }
}

/// Determine the severity tier for a 0-100 compliance score. Same
/// thresholds as the web's `lib/health-score.ts::scoreColor`.
({Color fg, String label}) complianceScoreTier(
  double score,
  KubeColors colors,
) {
  if (score >= 90) return (fg: colors.success, label: 'Healthy');
  if (score >= 70) return (fg: colors.warning, label: 'At risk');
  return (fg: colors.error, label: 'Critical');
}
