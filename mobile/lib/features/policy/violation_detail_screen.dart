// Single-violation context. Backend has no GET-by-id endpoint for
// violations, so this screen reads the violations list and filters by
// the stable key `policy|rule|namespace|kind|name`. When the list no
// longer contains the violation (e.g., remediated since the page was
// captured), the screen renders a "violation not found" empty state
// rather than a confusing blank tab.
//
// Target-resource link: routed through the generic catch-all
// (`/clusters/<id>/generic/<kind>/<ns>/<name>`) since policy targets may
// be CRDs outside `domainSections`. Users tapping a Pod / Deployment /
// Service violation still land on a working detail screen via the
// generic fallback.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../api/policy_repository.dart';
import '../../routing/domain_sections.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/empty_states.dart';
import 'policy_widgets.dart';

class ViolationDetailScreen extends ConsumerWidget {
  const ViolationDetailScreen({super.key, required this.stableKey});

  /// `policy|rule|namespace|kind|name` — round-trips via
  /// `PolicyViolation.stableKey`. Empty fields are preserved (the rule
  /// segment is empty for Gatekeeper violations).
  final String stableKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      appBar: AppBar(title: const Text('Violation')),
      body: PolicyStatusGate(
        builder: (clusterId, _) =>
            _DetailBody(clusterId: clusterId, stableKey: stableKey),
      ),
    );
  }
}

class _DetailBody extends ConsumerWidget {
  const _DetailBody({required this.clusterId, required this.stableKey});

  final String clusterId;
  final String stableKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(violationsListProvider(clusterId));
    final colors = Theme.of(context).extension<KubeColors>()!;

    return async.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => ErrorStateView(
        message: e.toString(),
        onRetry: () => ref.invalidate(violationsListProvider(clusterId)),
      ),
      data: (items) {
        PolicyViolation? violation;
        for (final v in items) {
          if (v.stableKey == stableKey) {
            violation = v;
            break;
          }
        }
        if (violation == null) {
          return EmptyState(
            title: 'Violation not found',
            message:
                'This violation may have been remediated. Pull-to-refresh '
                'the violations list to confirm.',
            icon: Icons.check_circle_outline,
          );
        }
        return _DetailContent(
          clusterId: clusterId,
          violation: violation,
          colors: colors,
        );
      },
    );
  }
}

class _DetailContent extends StatelessWidget {
  const _DetailContent({
    required this.clusterId,
    required this.violation,
    required this.colors,
  });

  final String clusterId;
  final PolicyViolation violation;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _Section(
          title: violation.policy,
          colors: colors,
          children: [
            if (violation.rule.isNotEmpty)
              _LabelValue(label: 'Rule', value: violation.rule, colors: colors),
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: Wrap(
                spacing: 6,
                runSpacing: 4,
                children: [
                  EngineBadge(engine: violation.engine),
                  SeverityBadge(severity: violation.severity),
                  BlockingBadge(blocking: violation.blocking),
                ],
              ),
            ),
            if (violation.message.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Text(
                  violation.message,
                  style: TextStyle(color: colors.textPrimary, fontSize: 13),
                ),
              ),
            if (violation.timestamp.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Text(
                  'Reported ${violation.timestamp}',
                  style: TextStyle(color: colors.textMuted, fontSize: 11),
                ),
              ),
          ],
        ),
        const SizedBox(height: 12),
        _Section(
          title: 'Target resource',
          colors: colors,
          children: [
            _LabelValue(label: 'Kind', value: violation.kind, colors: colors),
            _LabelValue(
              label: 'Namespace',
              value: violation.namespace.isEmpty
                  ? '(cluster-scoped)'
                  : violation.namespace,
              colors: colors,
            ),
            _LabelValue(label: 'Name', value: violation.name, colors: colors),
            const SizedBox(height: 8),
            FilledButton.icon(
              onPressed: violation.name.isEmpty
                  ? null
                  : () => context.push(_targetResourcePath()),
              icon: const Icon(Icons.open_in_new, size: 16),
              label: const Text('View target resource'),
            ),
          ],
        ),
        const SizedBox(height: 12),
        _Section(
          title: 'Remediation',
          colors: colors,
          children: [
            Text(
              _remediationHint(violation),
              style: TextStyle(color: colors.textSecondary, fontSize: 13),
            ),
          ],
        ),
      ],
    );
  }

  String _targetResourcePath() {
    // Route through the generic catch-all so any kind (including CRDs
    // outside `domainSections`) resolves to a working detail screen.
    // Cluster-scoped resources use the sentinel namespace segment.
    final ns = violation.namespace.isEmpty
        ? clusterScopedNamespaceSentinel
        : Uri.encodeComponent(violation.namespace);
    return '/clusters/$clusterId/generic/'
        '${Uri.encodeComponent(violation.kind)}/'
        '$ns/${Uri.encodeComponent(violation.name)}';
  }
}

/// Best-effort remediation copy derived from the engine + action. The
/// backend's `message` carries the rule-specific detail; this is the
/// generic "how to address" hint that sits next to it.
String _remediationHint(PolicyViolation v) {
  if (v.blocking) {
    return 'This is a blocking violation — the workload was rejected at '
        'admission. Update the manifest to satisfy the policy before '
        're-applying. View the policy detail to inspect the rule body.';
  }
  if (v.engine == PolicyEngine.kyverno) {
    return 'Audit-only violation. Kyverno is recording this for the '
        'compliance score but is not blocking writes. Update the manifest '
        'to satisfy the policy or switch the policy to enforce mode if '
        'you want admission rejected on the next write.';
  }
  if (v.engine == PolicyEngine.gatekeeper) {
    return 'Audit-only violation. Gatekeeper is recording this against '
        'the constraint without blocking admission. Update the manifest '
        'to satisfy the constraint or set `enforcementAction: deny` on '
        'the constraint to block future writes.';
  }
  return 'Update the manifest to satisfy the policy. The `message` field '
      'above carries the engine-specific reason.';
}

// ---------------------------------------------------------------------------
// Section + label helpers
// ---------------------------------------------------------------------------

class _Section extends StatelessWidget {
  const _Section({
    required this.title,
    required this.colors,
    required this.children,
  });

  final String title;
  final KubeColors colors;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 16,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          ...children,
        ],
      ),
    );
  }
}

class _LabelValue extends StatelessWidget {
  const _LabelValue({
    required this.label,
    required this.value,
    required this.colors,
  });

  final String label;
  final String value;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 88,
            child: Text(
              label,
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: TextStyle(color: colors.textPrimary, fontSize: 13),
            ),
          ),
        ],
      ),
    );
  }
}
