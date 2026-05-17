// Consistent "feature not installed" empty state shown when a CRD-
// discovered domain's status endpoint reports `detected: false`. Each
// domain has its own named factory constructor so call sites are a
// one-liner and the install message stays close to the domain
// definition rather than scattered across eight screens.

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';

/// Full-screen (centered) card shown when an optional cluster feature
/// is not detected. All eight M4 domains share this widget so the
/// install-guidance UX is visually consistent.
class FeatureUnavailableState extends StatelessWidget {
  const FeatureUnavailableState({
    required this.featureName,
    this.helpMessage,
    this.icon = Icons.extension_off_outlined,
    super.key,
  });

  final String featureName;
  final String? helpMessage;
  final IconData icon;

  // ---------------------------------------------------------------------------
  // Named factory constructors — one per CRD-discovered M4 domain
  // ---------------------------------------------------------------------------

  factory FeatureUnavailableState.monitoring() =>
      const FeatureUnavailableState(
        featureName: 'Prometheus monitoring',
        helpMessage:
            'Install kube-prometheus-stack on this cluster to enable metrics '
            'charts. Open k8sCenter on a desktop for installation guidance.',
        icon: Icons.show_chart_outlined,
      );

  factory FeatureUnavailableState.loki() => const FeatureUnavailableState(
        featureName: 'Loki log aggregation',
        helpMessage:
            'Install Loki (grafana/loki-stack) on this cluster to enable '
            'multi-pod log search. Single-pod live tail remains available '
            'from the pod detail screen.',
        icon: Icons.text_snippet_outlined,
      );

  factory FeatureUnavailableState.certManager() =>
      const FeatureUnavailableState(
        featureName: 'cert-manager',
        helpMessage:
            'Install cert-manager to enable certificate lifecycle management, '
            'expiry monitoring, and issuer wizards on this cluster.',
        icon: Icons.verified_outlined,
      );

  factory FeatureUnavailableState.eso() => const FeatureUnavailableState(
        featureName: 'External Secrets Operator',
        helpMessage:
            'Install the External Secrets Operator to sync secrets from '
            'Vault, AWS Secrets Manager, and other providers into this cluster.',
        icon: Icons.lock_person_outlined,
      );

  factory FeatureUnavailableState.policy() => const FeatureUnavailableState(
        featureName: 'policy engine (Kyverno or Gatekeeper)',
        helpMessage:
            'Install Kyverno or OPA Gatekeeper on this cluster to enable '
            'compliance scoring and policy violation browsing.',
        icon: Icons.policy_outlined,
      );

  factory FeatureUnavailableState.gitops() => const FeatureUnavailableState(
        featureName: 'GitOps controller (Argo CD or Flux CD)',
        helpMessage:
            'Install Argo CD or Flux CD on this cluster to enable application '
            'sync status, managed resource trees, and revision history.',
        icon: Icons.account_tree_outlined,
      );

  factory FeatureUnavailableState.mesh() => const FeatureUnavailableState(
        featureName: 'service mesh (Istio or Linkerd)',
        helpMessage:
            'Install Istio or Linkerd on this cluster to enable mTLS posture, '
            'traffic routing, and golden signal metrics per service.',
        icon: Icons.hub_outlined,
      );

  factory FeatureUnavailableState.scanning() => const FeatureUnavailableState(
        featureName: 'vulnerability scanner (Trivy or Kubescape)',
        helpMessage:
            'Install Trivy Operator or Kubescape on this cluster to enable '
            'image vulnerability reports and compliance benchmarks.',
        icon: Icons.security_outlined,
      );

  // ---------------------------------------------------------------------------
  // Build
  // ---------------------------------------------------------------------------

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;

    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Card(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                ExcludeSemantics(child: Icon(icon, size: 40, color: colors.textMuted)),
                const SizedBox(height: 16),
                Text(
                  featureName,
                  textAlign: TextAlign.center,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  'is not installed on this cluster',
                  textAlign: TextAlign.center,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 14,
                  ),
                ),
                if (helpMessage != null) ...[
                  const SizedBox(height: 12),
                  Text(
                    helpMessage!,
                    textAlign: TextAlign.center,
                    style: TextStyle(
                      color: colors.textMuted,
                      fontSize: 13,
                      height: 1.4,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
