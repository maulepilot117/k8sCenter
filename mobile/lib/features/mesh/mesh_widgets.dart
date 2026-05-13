// Shared widget primitives + colour helpers for the M4 service-mesh
// surfaces. Centralising these mirrors the precedent set by
// `features/gitops/gitops_widgets.dart` so list / detail / dashboard
// screens don't each carry a private `_StatusPill` copy.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

/// Coloured rounded pill — sync/mtls/state badges across all mesh
/// screens use this. Visually identical to `gitops.StatusPill` but
/// kept separate so future tweaks can diverge without coupling the
/// two domains.
class MeshPill extends StatelessWidget {
  const MeshPill({super.key, required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(10),
        color: color.withValues(alpha: 0.15),
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

/// Maps a mesh state / mTLS state string → KubeColors token.
/// Backend values are case-canonical (e.g. `"active"`, `"STRICT"`) but
/// we lowercase before matching so any backend casing tweak doesn't
/// produce silent color drift.
Color meshStateColor(KubeColors colors, String? raw) {
  return switch (raw?.toLowerCase() ?? '') {
    'active' || 'strict' || 'allow' || 'allow_all' || 'healthy' || 'synced' =>
      colors.success,
    'mixed' || 'permissive' || 'audit' || 'outofsync' => colors.warning,
    'inactive' || 'disable' || 'deny' || 'deny_all' || 'failed' => colors.error,
    'unmeshed' || 'unset' || 'suspended' || '' => colors.textMuted,
    _ => colors.textMuted,
  };
}

/// "Warn"-tier keys in a partial-failure errors map. Anything outside
/// this set surfaces as an error-coloured banner. Mirrors the web's
/// `WARN_KEYS = {"prometheus-cross-check", "truncated"}` exactly.
const Set<String> kMeshErrorWarnKeys = {
  'prometheus-cross-check',
  'truncated',
};

/// Renders the per-CRD partial-failure map as a stack of inline
/// banners. Warn-tier keys get a yellow background; everything else
/// is error-coloured. Returns an empty SizedBox when [errors] is empty
/// so callers can drop it into a Column without a conditional.
class MeshErrorsBanner extends StatelessWidget {
  const MeshErrorsBanner({super.key, required this.errors});

  final Map<String, String> errors;

  @override
  Widget build(BuildContext context) {
    if (errors.isEmpty) return const SizedBox.shrink();
    final colors = Theme.of(context).extension<KubeColors>()!;
    final entries = errors.entries.toList();
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final entry in entries)
            MeshBanner(
              icon: kMeshErrorWarnKeys.contains(entry.key)
                  ? Icons.info_outline
                  : Icons.warning_amber_outlined,
              color: kMeshErrorWarnKeys.contains(entry.key)
                  ? colors.warning
                  : colors.error,
              title: entry.key,
              body: entry.value,
            ),
        ],
      ),
    );
  }
}

/// Inline banner used across mesh screens to surface warnings and
/// partial-failure messages. [MeshErrorsBanner] uses this internally;
/// golden signals and other screens can reference it directly.
///
/// Named parameters follow the [_Banner] convention from
/// `golden_signals_tab.dart` (pre-consolidation) so callers do not
/// need to map `label`/`message` → `title`/`body`.
class MeshBanner extends StatelessWidget {
  const MeshBanner({
    super.key,
    required this.icon,
    required this.color,
    required this.title,
    required this.body,
  });

  final IconData icon;
  final Color color;
  final String title;
  final String body;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 4),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: color.withValues(alpha: 0.3)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, color: color, size: 16),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  title,
                  style: TextStyle(
                    color: color,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                if (body.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Text(
                      body,
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 11,
                      ),
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

/// Key/value line for the metadata panels on detail screens. Same
/// shape as `gitops.KvLine` but with a tighter label column — mesh
/// labels run short ("Mesh", "Kind", "Host") so we don't need the
/// 132px width that GitOps uses for "Destination cluster" etc.
class MeshKvLine extends StatelessWidget {
  const MeshKvLine({
    super.key,
    required this.label,
    required this.value,
    this.valueColor,
    this.monospace = false,
  });

  final String label;
  final String value;
  final Color? valueColor;
  final bool monospace;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 96,
            child: Text(
              label,
              style: TextStyle(color: colors.textMuted, fontSize: 13),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: TextStyle(
                color: valueColor ?? colors.textPrimary,
                fontSize: 13,
                fontFamily: monospace ? 'monospace' : null,
                fontWeight:
                    valueColor == null ? FontWeight.w400 : FontWeight.w600,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// Section card used across the mesh screens to group related fields.
/// Same chrome as the resource_detail_scaffold DetailSection but
/// re-implemented here to avoid pulling that file into the mesh
/// feature dependency surface (it carries YAML editor imports).
class MeshSection extends StatelessWidget {
  const MeshSection({super.key, required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: TextStyle(
              color: colors.textMuted,
              fontSize: 11,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.6,
            ),
          ),
          const SizedBox(height: 6),
          Card(
            margin: EdgeInsets.zero,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
              child: child,
            ),
          ),
        ],
      ),
    );
  }
}

/// Muted rounded badge — destination-count and scope labels across the
/// mesh routing and mTLS screens. Unlike [MeshPill], this badge uses a
/// plain background rather than a tinted colour fill, signalling that
/// the content is metadata rather than a status value.
class MeshMutedBadge extends StatelessWidget {
  const MeshMutedBadge({super.key, required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(10),
        color: colors.bgElevated,
      ),
      child: Text(
        label,
        style: TextStyle(color: colors.textMuted, fontSize: 11),
      ),
    );
  }
}

/// Human-readable label for a mesh discriminator. `"both"` reads as
/// `"Istio + Linkerd"` for operator clarity.
String meshDisplayName(String mesh) => switch (mesh) {
      'istio' => 'Istio',
      'linkerd' => 'Linkerd',
      'both' => 'Istio + Linkerd',
      '' => 'Unknown',
      _ => mesh,
    };
