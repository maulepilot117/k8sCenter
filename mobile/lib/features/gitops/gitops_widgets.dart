// Shared widget primitives and colour helpers for the GitOps detail
// surfaces. Centralising these removes four private `_StatusPill` /
// `_StatusBadge` copies and the two `_statusColor` / `_syncColor` /
// `_healthColor` duplicates that previously lived in each file.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

/// Coloured rounded pill used on list rows and detail screens to
/// show sync / health / generic status values.
class StatusPill extends StatelessWidget {
  const StatusPill({super.key, required this.label, required this.color});

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

/// Unified status-to-colour mapping used across all GitOps screens.
///
/// Always normalises via [String.toLowerCase] before the switch so
/// mixed-case values from the wire (e.g. `"Synced"`, `"OutOfSync"`)
/// are handled consistently. The function subsumes both the
/// sync/health/generic variants that previously diverged across
/// `applications_list_screen`, `application_detail_screen`,
/// `applicationsets_list_screen`, and `applicationset_detail_screen`.
Color statusColor(KubeColors colors, String? raw) {
  return switch (raw?.toLowerCase() ?? '') {
    'healthy' || 'synced' => colors.success,
    'outofsync' || 'out of sync' => colors.warning,
    'progressing' => colors.info,
    'degraded' ||
    'error' ||
    'errored' ||
    'failed' ||
    'stalled' =>
      colors.error,
    'suspended' => colors.textMuted,
    _ => colors.textMuted,
  };
}

/// Key-value row widget shared between [ApplicationDetailScreen] and
/// [ApplicationSetDetailScreen].
///
/// Uses a flat-string signature (`label` / `value` / optional
/// `valueColor`) instead of the object-wrapper approach in
/// `application_detail_screen`'s previous `_KvRow` / `_KvLine` pair.
class KvLine extends StatelessWidget {
  const KvLine({
    super.key,
    required this.label,
    required this.value,
    this.valueColor,
  });

  final String label;
  final String value;
  final Color? valueColor;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 132,
            child: Text(
              label,
              style: TextStyle(color: colors.textMuted, fontSize: 13),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: TextStyle(
                color: valueColor ?? colors.textPrimary,
                fontSize: 13,
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
