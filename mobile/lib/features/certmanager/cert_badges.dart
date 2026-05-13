// Status / expiry / issuer-type badges shared across the cert-manager
// surfaces (list, detail, expiring view, issuers list). Mirrors the web's
// `frontend/components/ui/CertificateBadges.tsx`, with one upgrade:
// expiry color uses the backend-resolved threshold pair rather than
// hardcoded 7d/30d cutoffs. The plan (PR-4g) explicitly requires this so
// per-cert / per-issuer annotation overrides flow through to the badge.
//
// Color contract:
//   * `success`        — days remaining > warn threshold (informational)
//   * `warning`        — critical < days remaining ≤ warn
//   * `error`          — days remaining ≤ critical (or already expired)
//   * `textMuted`      — daysRemaining is null (cert not yet signed)

import 'package:flutter/material.dart';

import '../../api/certmanager_repository.dart';
import '../../theme/kube_theme_builder.dart';

/// Package-default warn threshold, matching `WarningThresholdDays` in
/// `backend/internal/certmanager/types.go`. Used as the fallback when the
/// backend response doesn't carry a resolved threshold (older callers,
/// or the `/expiring` endpoint which never embeds them).
const int kDefaultWarningThresholdDays = 30;

/// Package-default critical threshold, matching `CriticalThresholdDays`
/// in `backend/internal/certmanager/types.go`.
const int kDefaultCriticalThresholdDays = 7;

/// Pill badge for [CertStatus]. Color contract matches the web's
/// `STATUS_COLORS` map: Ready=success, Issuing=accent, Failed=error,
/// Expired=error, Expiring=warning, Unknown=textMuted.
class CertStatusPill extends StatelessWidget {
  const CertStatusPill({super.key, required this.status});

  final CertStatus status;

  Color _color(KubeColors c) => switch (status) {
        CertStatus.ready => c.success,
        CertStatus.issuing => c.accent,
        CertStatus.failed => c.error,
        CertStatus.expired => c.error,
        CertStatus.expiring => c.warning,
        CertStatus.unknown => c.textMuted,
      };

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final tone = _color(colors);
    return _Pill(label: certStatusLabel(status), color: tone);
  }
}

/// Badge for an Issuer's [type] (ACME / CA / Vault / SelfSigned /
/// Unknown). Open enum — unknown types render as muted.
class IssuerTypeBadge extends StatelessWidget {
  const IssuerTypeBadge({super.key, required this.type});

  final String type;

  Color _color(KubeColors c) => switch (type) {
        'ACME' => c.accent,
        'CA' => c.success,
        'Vault' => c.warning,
        'SelfSigned' => c.textMuted,
        _ => c.textMuted,
      };

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return _Pill(
      label: type.isEmpty ? 'Unknown' : type,
      color: _color(colors),
    );
  }
}

/// Days-remaining badge — colored from the resolved warn/critical
/// thresholds rather than hardcoded cutoffs. When the backend response
/// elides the threshold pair the package defaults (30d warn, 7d crit)
/// stand in so the color remains operator-meaningful.
class ExpiryBadge extends StatelessWidget {
  const ExpiryBadge({
    super.key,
    required this.daysRemaining,
    this.warningThresholdDays,
    this.criticalThresholdDays,
  });

  final int? daysRemaining;
  final int? warningThresholdDays;
  final int? criticalThresholdDays;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final d = daysRemaining;
    if (d == null) {
      return Text('—', style: TextStyle(color: colors.textMuted, fontSize: 12));
    }
    final warn = warningThresholdDays ?? kDefaultWarningThresholdDays;
    final crit = criticalThresholdDays ?? kDefaultCriticalThresholdDays;
    if (d < 0) return _Pill(label: 'Expired', color: colors.error);
    if (d <= crit) return _Pill(label: '${d}d left', color: colors.error);
    if (d <= warn) return _Pill(label: '${d}d left', color: colors.warning);
    return _Pill(label: '${d}d', color: colors.success);
  }
}

/// Ready/not-ready badge for Issuers and ClusterIssuers.
/// Replaces the private `_ReadyBadge` in `issuers_list_screen.dart`.
class ReadyBadge extends StatelessWidget {
  const ReadyBadge({super.key, required this.ready});

  final bool ready;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final tone = ready ? colors.success : colors.error;
    return _Pill(label: ready ? 'Ready' : 'Not ready', color: tone);
  }
}

/// Pill chrome — same shape used by every cert-manager badge so colors
/// vary but the geometry stays consistent.
class _Pill extends StatelessWidget {
  const _Pill({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
