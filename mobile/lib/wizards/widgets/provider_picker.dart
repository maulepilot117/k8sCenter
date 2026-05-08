// SecretStore provider picker. Renders a list of supported provider
// IDs with a label + short description. Sort order mirrors the web
// frontend: popular first (Vault, AWS, GCP, Azure), then alphabetical.
//
// Only the 8 providers that ship guided forms are listed. The other 3
// CRD-supported providers (bitwardensecretsmanager, conjur, infisical)
// are accessible via the YAML editor — both backend wizard and web
// frontend reject those without a registered form anyway.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';
import 'section_header.dart';

class SecretStoreProvider {
  const SecretStoreProvider({
    required this.id,
    required this.label,
    required this.description,
  });

  /// Wire identifier used by `secretstore.go` (`vault`, `aws`, `awsps`,
  /// `azurekv`, `gcpsm`, `kubernetes`, `doppler`, `onepassword`).
  final String id;
  final String label;
  final String description;
}

/// Popular-first then alphabetical, matching the web frontend's
/// `frontend/islands/SecretStoreWizard.tsx` `PROVIDER_FORMS` order.
const List<SecretStoreProvider> kSecretStoreProviders = [
  SecretStoreProvider(
    id: 'vault',
    label: 'HashiCorp Vault',
    description: 'KV v1/v2 with token, kubernetes, AppRole, JWT, or cert auth.',
  ),
  SecretStoreProvider(
    id: 'aws',
    label: 'AWS Secrets Manager',
    description: 'IRSA or static credentials.',
  ),
  SecretStoreProvider(
    id: 'awsps',
    label: 'AWS Parameter Store',
    description: 'IRSA or static credentials.',
  ),
  SecretStoreProvider(
    id: 'gcpsm',
    label: 'GCP Secret Manager',
    description: 'Workload Identity or service-account key.',
  ),
  SecretStoreProvider(
    id: 'azurekv',
    label: 'Azure Key Vault',
    description: 'Managed identity, workload identity, or service principal.',
  ),
  SecretStoreProvider(
    id: 'kubernetes',
    label: 'Kubernetes (cross-cluster)',
    description: 'Read Secrets from a remote Kubernetes cluster.',
  ),
  SecretStoreProvider(
    id: 'doppler',
    label: 'Doppler',
    description: 'Project + config with token or OIDC auth.',
  ),
  SecretStoreProvider(
    id: 'onepassword',
    label: '1Password Connect',
    description: 'Connect host + Connect token.',
  ),
];

/// Convenience lookup. Returns null when the id isn't a registered
/// provider — the screen treats that as "no form ready" and renders
/// the YAML-editor fallback message.
SecretStoreProvider? findSecretStoreProvider(String id) {
  for (final p in kSecretStoreProviders) {
    if (p.id == id) return p;
  }
  return null;
}

class ProviderPicker extends StatelessWidget {
  const ProviderPicker({
    super.key,
    required this.selected,
    required this.onChanged,
    this.errorMessage,
  });

  /// Currently picked provider id, or empty string for none.
  final String selected;
  final ValueChanged<String> onChanged;
  final String? errorMessage;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader(
          'Provider',
          subtitle: 'Pick the backend this SecretStore reads from',
        ),
        const SizedBox(height: 8),
        for (final p in kSecretStoreProviders) ...[
          _ProviderTile(
            provider: p,
            selected: p.id == selected,
            onTap: () => onChanged(p.id),
          ),
          const SizedBox(height: 6),
        ],
        if (errorMessage != null) ...[
          const SizedBox(height: 8),
          Text(
            errorMessage!,
            style: TextStyle(color: colors.error, fontSize: 12),
          ),
        ],
      ],
    );
  }
}

class _ProviderTile extends StatelessWidget {
  const _ProviderTile({
    required this.provider,
    required this.selected,
    required this.onTap,
  });

  final SecretStoreProvider provider;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          border: Border.all(
            color: selected ? colors.accent : colors.borderSubtle,
            width: selected ? 2 : 1,
          ),
          borderRadius: BorderRadius.circular(8),
          color: selected
              ? colors.accent.withValues(alpha: 0.08)
              : Colors.transparent,
        ),
        child: Row(
          children: [
            Icon(
              selected
                  ? Icons.radio_button_checked
                  : Icons.radio_button_unchecked,
              size: 20,
              color: selected ? colors.accent : colors.textMuted,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    provider.label,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    provider.description,
                    style: TextStyle(
                      color: colors.textMuted,
                      fontSize: 12,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
