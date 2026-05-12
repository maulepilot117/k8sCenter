// Renders the rules-engine output as a two-section list: failed checks
// expanded with full detail, passed checks collapsed into a compact
// strip. Mirrors `frontend/islands/DiagnosticChecklist.tsx`.
//
// Severity tokens map exclusively through `KubeColors` — no hardcoded
// `Color(0xFF...)` literals. The chip colours use *Dim backgrounds to
// keep the failed cards readable in both light and dark themes
// without the chip blending into the card surface.
//
// Linked-resource chips on a failed rule navigate via `kindDetailPath`
// to that resource's detail screen. The backend currently emits links
// with kinds in the `kindToResource` map (Pod, Deployment, etc.) plus
// the synthetic "View Logs" label which web special-cases to the log
// search route. On mobile we route logs into the M4 PR-4c LogSearch
// surface — same destination, shorter path because there's only one
// log-search screen.

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../../api/diagnostics_repository.dart';
import '../../../routing/domain_sections.dart';
import '../../../theme/kube_theme_builder.dart';

class DiagnosticChecklist extends StatelessWidget {
  const DiagnosticChecklist({
    super.key,
    required this.clusterId,
    required this.namespace,
    required this.failed,
    required this.passed,
  });

  final String clusterId;
  final String namespace;
  final List<DiagnosticResult> failed;
  final List<DiagnosticResult> passed;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _SectionHeader(label: 'FAILED CHECKS (${failed.length})'),
        const SizedBox(height: 8),
        if (failed.isEmpty)
          _SuccessBanner(
            message: passed.isEmpty
                ? 'No diagnostic rules apply to this resource'
                : 'No issues detected — all checks passed',
          )
        else
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (final r in failed)
                Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: _FailedCard(
                    result: r,
                    clusterId: clusterId,
                    namespace: namespace,
                  ),
                ),
            ],
          ),
        const SizedBox(height: 20),
        _SectionHeader(label: 'PASSED CHECKS (${passed.length})'),
        const SizedBox(height: 8),
        if (passed.isEmpty)
          Text(
            'No passing checks recorded',
            style: TextStyle(color: colors.textMuted, fontSize: 13),
          )
        else
          Container(
            decoration: BoxDecoration(
              color: colors.bgSurface,
              border: Border.all(color: colors.borderSubtle),
              borderRadius: BorderRadius.circular(8),
            ),
            child: Column(
              children: [
                for (var i = 0; i < passed.length; i++)
                  _PassedRow(
                    result: passed[i],
                    isLast: i == passed.length - 1,
                  ),
              ],
            ),
          ),
      ],
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.label});

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

class _SuccessBanner extends StatelessWidget {
  const _SuccessBanner({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      decoration: BoxDecoration(
        color: colors.successDim,
        border: Border.all(color: colors.success.withValues(alpha: 0.4)),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Icon(Icons.check_circle_outline,
              color: colors.success, size: 18),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              message,
              style: TextStyle(
                color: colors.success,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _FailedCard extends StatelessWidget {
  const _FailedCard({
    required this.result,
    required this.clusterId,
    required this.namespace,
  });

  final DiagnosticResult result;
  final String clusterId;
  final String namespace;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final severityColor = _severityColor(result.severity, colors);
    final iconColor = result.status == 'fail' ? colors.error : colors.warning;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                result.status == 'fail'
                    ? Icons.cancel_outlined
                    : Icons.warning_amber_outlined,
                size: 16,
                color: iconColor,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  result.ruleName,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  color: _severityBg(result.severity, colors),
                  borderRadius: BorderRadius.circular(10),
                ),
                child: Text(
                  result.severity,
                  style: TextStyle(
                    color: severityColor,
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          SelectableText(
            result.message,
            style: TextStyle(color: colors.textPrimary, fontSize: 13),
          ),
          if ((result.detail ?? '').isNotEmpty) ...[
            const SizedBox(height: 6),
            SelectableText(
              result.detail!,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ],
          if ((result.remediation ?? '').isNotEmpty) ...[
            const SizedBox(height: 8),
            Container(
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
              decoration: BoxDecoration(
                color: colors.bgElevated,
                borderRadius: BorderRadius.circular(6),
              ),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Icon(Icons.lightbulb_outline,
                      size: 14, color: colors.accent),
                  const SizedBox(width: 8),
                  Expanded(
                    child: SelectableText(
                      result.remediation!,
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 12,
                        fontStyle: FontStyle.italic,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
          if (result.links.isNotEmpty) ...[
            const SizedBox(height: 10),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final link in result.links)
                  _LinkChip(
                    link: link,
                    clusterId: clusterId,
                    namespace: namespace,
                  ),
              ],
            ),
          ],
        ],
      ),
    );
  }

  Color _severityColor(String severity, KubeColors c) {
    switch (severity) {
      case 'critical':
        return c.error;
      case 'warning':
        return c.warning;
      default:
        return c.textMuted;
    }
  }

  Color _severityBg(String severity, KubeColors c) {
    switch (severity) {
      case 'critical':
        return c.errorDim;
      case 'warning':
        return c.warningDim;
      default:
        return c.bgElevated;
    }
  }
}

class _PassedRow extends StatelessWidget {
  const _PassedRow({required this.result, required this.isLast});

  final DiagnosticResult result;
  final bool isLast;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        border: isLast
            ? null
            : Border(
                bottom: BorderSide(color: colors.borderSubtle),
              ),
      ),
      child: Row(
        children: [
          Icon(Icons.check, color: colors.success, size: 16),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              result.ruleName,
              style: TextStyle(
                color: colors.success,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          Flexible(
            child: Text(
              result.message,
              textAlign: TextAlign.right,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}

class _LinkChip extends StatelessWidget {
  const _LinkChip({
    required this.link,
    required this.clusterId,
    required this.namespace,
  });

  final DiagnosticLink link;
  final String clusterId;
  final String namespace;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return InkWell(
      borderRadius: BorderRadius.circular(12),
      onTap: () => _navigate(context),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
        decoration: BoxDecoration(
          color: colors.accentDim,
          border: Border.all(color: colors.accent.withValues(alpha: 0.4)),
          borderRadius: BorderRadius.circular(12),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.open_in_new, color: colors.accent, size: 12),
            const SizedBox(width: 6),
            Text(
              link.label,
              style: TextStyle(
                color: colors.accent,
                fontSize: 12,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _navigate(BuildContext context) {
    // Backend synthesises "View Logs" links with `kind: "Pod"` /
    // `name: <pod>`. Mobile routes these to the multi-pod LogQL search
    // surface (PR-4c) seeded with the namespace; the single-pod live
    // tail requires a container name the diagnostics payload doesn't
    // carry, so this is the correct deep-link target.
    if (link.label == 'View Logs') {
      context.push('/clusters/$clusterId/logs?namespace=$namespace');
      return;
    }
    final kindSeg = _kindRouteSegment(link.kind);
    final path = kindDetailPath(
      clusterId: clusterId,
      kind: kindSeg,
      namespace: namespace,
      name: link.name,
    );
    context.push(path);
  }

  /// Map canonical Kubernetes Kind to the route segment used by
  /// `kindDetailPath`. The diagnostics backend emits canonical Kind
  /// names ("Pod", "Service"), but the routing table keys on the URL
  /// segment ("pods", "services"). Mirrors `KIND_ROUTE_MAP` in
  /// `frontend/lib/types/diagnostics.ts`.
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
      case 'Job':
        return 'jobs';
      case 'CronJob':
        return 'cronjobs';
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
      case 'Endpoints':
        return 'endpoints';
    }
    // Unknown kind falls through to the generic-detail catch-all; pass
    // the canonical name through and let `kindDetailPath` route to
    // `/generic/...`.
    return canonical;
  }
}
