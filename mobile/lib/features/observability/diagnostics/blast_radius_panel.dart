// Two-section flat-list rendering of the backend's BlastResult.
// Mirrors `frontend/islands/BlastRadiusPanel.tsx` — explicitly NOT the
// graph view, per the M4 PR-4d scope. Each section uses ListView.builder
// for virtualization so a 100+-row blast radius doesn't stall on a
// long-tail dependency fan-out.
//
// Sort order (per the plan's "approach"):
//   1. Failing health rows first (failed nodes are the operator's
//      anchor; healthy children clutter the top of the screen if not
//      pushed down).
//   2. Degraded second.
//   3. Healthy / unknown last.
//   4. Alphabetical by name within each health bucket so refreshes
//      don't shuffle rows that haven't actually changed.

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../../api/diagnostics_repository.dart';
import '../../../routing/domain_sections.dart';
import '../../../theme/kube_theme_builder.dart';

class BlastRadiusPanel extends StatelessWidget {
  const BlastRadiusPanel({
    super.key,
    required this.clusterId,
    required this.namespace,
    required this.blastRadius,
  });

  final String clusterId;
  final String namespace;
  final BlastRadius blastRadius;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final direct = sortAffected(blastRadius.directlyAffected);
    final potential = sortAffected(blastRadius.potentiallyAffected);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _SectionLabel(label: 'BLAST RADIUS'),
        const SizedBox(height: 8),
        if (blastRadius.isEmpty)
          Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: colors.bgSurface,
              border: Border.all(color: colors.borderSubtle),
              borderRadius: BorderRadius.circular(8),
            ),
            child: Row(
              children: [
                Icon(Icons.check_circle_outline,
                    color: colors.success, size: 18),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(
                    'No dependent resources detected — failure of this '
                    'resource is contained to itself.',
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 13,
                    ),
                  ),
                ),
              ],
            ),
          )
        else ...[
          _BlastSection(
            title: 'Directly Affected',
            accentColor: colors.error,
            resources: direct,
            emptyMessage: 'No downstream resources',
            clusterId: clusterId,
            namespace: namespace,
          ),
          const SizedBox(height: 12),
          _BlastSection(
            title: 'Potentially Affected',
            accentColor: colors.warning,
            resources: potential,
            emptyMessage: 'No upstream resources',
            clusterId: clusterId,
            namespace: namespace,
          ),
        ],
      ],
    );
  }
}

/// Sort: failing health buckets first, then degraded, then healthy /
/// unknown last. Within each bucket, alphabetical by name. Exposed so
/// the panel test can verify ordering without rebuilding the widget.
@visibleForTesting
List<AffectedResource> sortAffected(List<AffectedResource> input) {
  final out = [...input];
  out.sort((a, b) {
    final ha = _healthRank(a.health);
    final hb = _healthRank(b.health);
    if (ha != hb) return ha.compareTo(hb);
    return a.name.compareTo(b.name);
  });
  return out;
}

int _healthRank(String health) {
  switch (health) {
    case 'failing':
      return 0;
    case 'degraded':
      return 1;
    case 'healthy':
      return 2;
    default:
      return 3;
  }
}

class _BlastSection extends StatelessWidget {
  const _BlastSection({
    required this.title,
    required this.accentColor,
    required this.resources,
    required this.emptyMessage,
    required this.clusterId,
    required this.namespace,
  });

  final String title;
  final Color accentColor;
  final List<AffectedResource> resources;
  final String emptyMessage;
  final String clusterId;
  final String namespace;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      decoration: BoxDecoration(
        color: colors.bgSurface,
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(
              horizontal: 14,
              vertical: 10,
            ),
            child: Row(
              children: [
                // Inline dot indicator instead of a left-edge stripe —
                // Flutter forbids `borderRadius` alongside a non-uniform
                // `Border`, and an IntrinsicHeight wrap to position a
                // stripe sibling breaks under the `ListView.builder`
                // virtualization the empty/non-empty body switches into.
                Container(
                  width: 8,
                  height: 8,
                  decoration: BoxDecoration(
                    color: accentColor,
                    shape: BoxShape.circle,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  title,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    letterSpacing: 0.5,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  '(${resources.length})',
                  style: TextStyle(color: colors.textMuted, fontSize: 12),
                ),
              ],
            ),
          ),
          Divider(height: 1, color: colors.borderSubtle),
          if (resources.isEmpty)
            Padding(
              padding: const EdgeInsets.all(14),
              child: Text(
                emptyMessage,
                style: TextStyle(color: colors.textMuted, fontSize: 13),
              ),
            )
          else
            // ListView.builder guards the 100+-row case from
            // materializing every row eagerly. Wrapped in
            // ConstrainedBox so the surrounding scroll view (the
            // diagnostics screen) can host the section without an
            // infinite-height assertion.
            ConstrainedBox(
              constraints: const BoxConstraints(maxHeight: 480),
              child: ListView.separated(
                shrinkWrap: true,
                physics: const ClampingScrollPhysics(),
                padding: EdgeInsets.zero,
                itemCount: resources.length,
                separatorBuilder: (_, _) => Divider(
                  height: 1,
                  color: colors.borderSubtle,
                ),
                itemBuilder: (context, i) => _AffectedRow(
                  resource: resources[i],
                  clusterId: clusterId,
                  namespace: namespace,
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _AffectedRow extends StatelessWidget {
  const _AffectedRow({
    required this.resource,
    required this.clusterId,
    required this.namespace,
  });

  final AffectedResource resource;
  final String clusterId;
  final String namespace;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final healthColor = _healthColor(resource.health, colors);
    final healthBg = _healthBg(resource.health, colors);
    return InkWell(
      onTap: () => _navigate(context),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        child: Row(
          children: [
            Container(
              padding: const EdgeInsets.symmetric(
                horizontal: 6,
                vertical: 2,
              ),
              decoration: BoxDecoration(
                color: colors.bgElevated,
                borderRadius: BorderRadius.circular(4),
              ),
              constraints: const BoxConstraints(minWidth: 60),
              child: Text(
                resource.kind,
                textAlign: TextAlign.center,
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 10,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                resource.name,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  color: colors.accent,
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            const SizedBox(width: 8),
            Container(
              padding: const EdgeInsets.symmetric(
                horizontal: 8,
                vertical: 2,
              ),
              decoration: BoxDecoration(
                color: healthBg,
                borderRadius: BorderRadius.circular(10),
              ),
              child: Text(
                resource.health,
                style: TextStyle(
                  color: healthColor,
                  fontSize: 11,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _navigate(BuildContext context) {
    final seg = _kindRouteSegment(resource.kind);
    final path = kindDetailPath(
      clusterId: clusterId,
      kind: seg,
      namespace: namespace,
      name: resource.name,
    );
    context.push(path);
  }

  String _kindRouteSegment(String canonical) {
    switch (canonical) {
      case 'Pod':
        return 'pods';
      case 'Deployment':
        return 'deployments';
      case 'StatefulSet':
        return 'statefulsets';
      case 'DaemonSet':
        return 'daemonsets';
      case 'Service':
        return 'services';
      case 'PersistentVolumeClaim':
        return 'pvcs';
      case 'ReplicaSet':
        return 'replicasets';
      case 'Ingress':
        return 'ingresses';
      case 'ConfigMap':
        return 'configmaps';
      case 'Secret':
        return 'secrets';
    }
    return canonical;
  }

  Color _healthColor(String health, KubeColors c) {
    switch (health) {
      case 'healthy':
        return c.success;
      case 'degraded':
        return c.warning;
      case 'failing':
        return c.error;
      default:
        return c.textMuted;
    }
  }

  Color _healthBg(String health, KubeColors c) {
    switch (health) {
      case 'healthy':
        return c.successDim;
      case 'degraded':
        return c.warningDim;
      case 'failing':
        return c.errorDim;
      default:
        return c.bgElevated;
    }
  }
}

class _SectionLabel extends StatelessWidget {
  const _SectionLabel({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Text(
      label,
      style: TextStyle(
        color: colors.textSecondary,
        fontSize: 12,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.5,
      ),
    );
  }
}
