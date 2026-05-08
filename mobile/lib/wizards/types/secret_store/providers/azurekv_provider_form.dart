// Azure Key Vault provider form for the SecretStore wizard. Stateless dumb
// widget — no riverpod, no dio.
//
// Wire format (spec.provider.azurekv shape mirrored from ESO):
//   {
//     vaultUrl:   String,               // required, https://
//     authType:   String,               // "ManagedIdentity" | "ServicePrincipal" | "WorkloadIdentity"
//
//     // ManagedIdentity only (both optional):
//     identityId?: String,              // optional client ID for multi-identity pods
//
//     // ServicePrincipal only:
//     tenantId:    String,              // required
//     authSecretRef: {
//       clientId:     { name, key },    // required
//       clientSecret: { name, key },    // required
//     }
//
//     // WorkloadIdentity only:
//     tenantId:          String,        // required
//     serviceAccountRef: { name },      // required
//   }
//
// Azure's auth discriminator is a top-level `authType` string — NOT a nested
// auth sub-block like Vault/AWS. All auth credential fields are sibling keys
// of `authType` at the spec root.
//
// Switching authType clears all auth-owned fields (tenantId, authSecretRef,
// serviceAccountRef, identityId) to prevent stale values bleeding through.
// Mirrors AzureKVForm.tsx setAuthType + AUTH_TYPE_OWNED_FIELDS exactly.
//
// Backend validator: backend/internal/wizard/secretstore_azurekv.go
// Web ground-truth:  frontend/components/wizard/secretstore/AzureKVForm.tsx
//
// Default authType: WorkloadIdentity (modern AKS recommended pattern).

import 'package:flutter/material.dart';

import 'provider_form.dart';
import '../../../widgets/section_header.dart';

// ---------------------------------------------------------------------------
// Auth type descriptor
// ---------------------------------------------------------------------------

const _kAuthTypes = [
  _AuthType(
    'ManagedIdentity',
    'Managed Identity',
    'Use the AKS-assigned managed identity. No credentials required.',
  ),
  _AuthType(
    'ServicePrincipal',
    'Service Principal',
    'App registration with client ID + secret stored in a K8s Secret.',
  ),
  _AuthType(
    'WorkloadIdentity',
    'Workload Identity',
    'AKS Workload Identity via a federated service account (no long-lived secret).',
  ),
];

/// Fields to clear when the authType changes. Mirrors AUTH_TYPE_OWNED_FIELDS
/// in AzureKVForm.tsx so stale auth fields never leak into the preview.
const _kAuthTypeOwnedFields = [
  'tenantId',
  'authSecretRef',
  'serviceAccountRef',
  'identityId',
];

class _AuthType {
  const _AuthType(this.id, this.label, this.description);
  final String id;
  final String label;
  final String description;
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/// Top-level builder for the Azure Key Vault provider form.
Widget azurekvProviderForm(ProviderFormProps props) =>
    _AzureKVProviderForm(props: props);

// ---------------------------------------------------------------------------
// Root stateful widget
// ---------------------------------------------------------------------------

class _AzureKVProviderForm extends StatefulWidget {
  const _AzureKVProviderForm({required this.props});
  final ProviderFormProps props;

  @override
  State<_AzureKVProviderForm> createState() => _AzureKVProviderFormState();
}

class _AzureKVProviderFormState extends State<_AzureKVProviderForm> {
  String _detectAuthType(Map<String, dynamic> spec) {
    final v = spec['authType'];
    if (v == 'ManagedIdentity' ||
        v == 'ServicePrincipal' ||
        v == 'WorkloadIdentity') {
      return v as String;
    }
    return 'WorkloadIdentity';
  }

  void _setAuthType(String authType) {
    final spec = widget.props.spec;
    if (_detectAuthType(spec) == authType) return;
    // Clear all auth-type-owned fields and set the new authType.
    final next = Map<String, dynamic>.from(spec);
    for (final f in _kAuthTypeOwnedFields) {
      next.remove(f);
    }
    next['authType'] = authType;
    widget.props.onUpdateSpec(next);
  }

  /// Patch auth.authSecretRef[field][subKey].
  void _patchAuthSecretRef(
      String field, String subKey, String value) {
    final spec = widget.props.spec;
    final existing = Map<String, dynamic>.from(
        spec['authSecretRef'] is Map<String, dynamic>
            ? spec['authSecretRef'] as Map<String, dynamic>
            : <String, dynamic>{});
    final ref = Map<String, dynamic>.from(
        existing[field] is Map<String, dynamic>
            ? existing[field] as Map<String, dynamic>
            : <String, dynamic>{});
    if (value.isEmpty) {
      ref.remove(subKey);
    } else {
      ref[subKey] = value;
    }
    existing[field] = ref;
    widget.props.onUpdateSpec(
        Map<String, dynamic>.from(spec)..['authSecretRef'] = existing);
  }

  /// Patch serviceAccountRef.name.
  void _patchSARefName(String value) {
    final spec = widget.props.spec;
    final existing = Map<String, dynamic>.from(
        spec['serviceAccountRef'] is Map<String, dynamic>
            ? spec['serviceAccountRef'] as Map<String, dynamic>
            : <String, dynamic>{});
    if (value.isEmpty) {
      existing.remove('name');
    } else {
      existing['name'] = value;
    }
    widget.props.onUpdateSpec(
        Map<String, dynamic>.from(spec)..['serviceAccountRef'] = existing);
  }

  @override
  Widget build(BuildContext context) {
    final props = widget.props;
    final spec = props.spec;
    final errors = props.errors;
    final activeAuthType = _detectAuthType(spec);

    // Read authSecretRef sub-maps for ServicePrincipal.
    final authSecretRef = spec['authSecretRef'];
    final authSecretRefMap = authSecretRef is Map<String, dynamic>
        ? authSecretRef
        : <String, dynamic>{};
    final clientIdRef = authSecretRefMap['clientId'];
    final clientIdRefMap = clientIdRef is Map<String, dynamic>
        ? clientIdRef
        : <String, dynamic>{};
    final clientSecretRef = authSecretRefMap['clientSecret'];
    final clientSecretRefMap = clientSecretRef is Map<String, dynamic>
        ? clientSecretRef
        : <String, dynamic>{};

    // Read serviceAccountRef.name for WorkloadIdentity.
    final saRefRaw = spec['serviceAccountRef'];
    final saRefMap =
        saRefRaw is Map<String, dynamic> ? saRefRaw : <String, dynamic>{};
    final saName = saRefMap['name'] is String ? saRefMap['name'] as String : '';

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // Info banner
        Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            border: Border.all(
                color: Theme.of(context).colorScheme.outlineVariant),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Text(
            'Configure the Azure Key Vault connection and authentication type. '
            'Credentials must already exist as Kubernetes Secrets in this namespace; '
            'this wizard only references them.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: 16),

        // --- Vault URL ---
        const WizardSectionHeader('Connection'),
        const SizedBox(height: 8),
        _TextField(
          value: props.getString('vaultUrl'),
          label: 'Vault URL',
          hint: 'https://my-vault.vault.azure.net',
          helper: 'Must use https. Typically ends in .vault.azure.net.',
          errorText: errors['vaultUrl'],
          onChanged: (v) => props.patchTop('vaultUrl', v),
        ),
        const SizedBox(height: 20),

        // --- Auth type picker ---
        const WizardSectionHeader(
          'Authentication type',
          subtitle: 'Select one — switching clears auth fields.',
        ),
        const SizedBox(height: 8),
        Column(
          children: [
            for (final t in _kAuthTypes) ...[
              _AuthTypeChip(
                authType: t,
                selected: activeAuthType == t.id,
                onTap: () => _setAuthType(t.id),
              ),
              const SizedBox(height: 8),
            ],
          ],
        ),
        if (errors['authType'] != null) ...[
          const SizedBox(height: 6),
          Text(errors['authType']!,
              style: TextStyle(
                  color: Theme.of(context).colorScheme.error, fontSize: 12)),
        ],
        const SizedBox(height: 8),

        // --- Per-type fields ---
        if (activeAuthType == 'ManagedIdentity')
          _AuthBox(
            title: 'Managed Identity',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'No credentials required. ESO uses the managed identity bound to '
                  'the controller pod by AKS. If multiple managed identities are '
                  'assigned, specify the client ID below.',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 12),
                _TextField(
                  value: props.getString('identityId'),
                  label: 'Identity client ID (optional)',
                  hint: 'xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx',
                  helper: 'Leave blank to use the AKS-default managed identity.',
                  errorText: errors['identityId'],
                  onChanged: (v) => props.patchTop('identityId', v),
                ),
              ],
            ),
          ),

        if (activeAuthType == 'ServicePrincipal')
          _AuthBox(
            title: 'Service Principal',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _TextField(
                  value: props.getString('tenantId'),
                  label: 'Tenant ID',
                  hint: 'xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx',
                  helper: 'Azure AD tenant ID (Directory ID).',
                  errorText: errors['tenantId'],
                  onChanged: (v) => props.patchTop('tenantId', v),
                ),
                const SizedBox(height: 12),
                Text('Client ID Secret reference',
                    style: Theme.of(context)
                        .textTheme
                        .labelMedium
                        ?.copyWith(fontWeight: FontWeight.w600)),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: _TextField(
                        value: clientIdRefMap['name'] is String
                            ? clientIdRefMap['name'] as String
                            : '',
                        label: 'Secret name',
                        hint: 'azure-sp-secret',
                        errorText: errors['authSecretRef.clientId.name'],
                        onChanged: (v) =>
                            _patchAuthSecretRef('clientId', 'name', v),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _TextField(
                        value: clientIdRefMap['key'] is String
                            ? clientIdRefMap['key'] as String
                            : '',
                        label: 'Key',
                        hint: 'client-id',
                        errorText: errors['authSecretRef.clientId.key'],
                        onChanged: (v) =>
                            _patchAuthSecretRef('clientId', 'key', v),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                Text('Client Secret reference',
                    style: Theme.of(context)
                        .textTheme
                        .labelMedium
                        ?.copyWith(fontWeight: FontWeight.w600)),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: _TextField(
                        value: clientSecretRefMap['name'] is String
                            ? clientSecretRefMap['name'] as String
                            : '',
                        label: 'Secret name',
                        hint: 'azure-sp-secret',
                        errorText: errors['authSecretRef.clientSecret.name'],
                        onChanged: (v) =>
                            _patchAuthSecretRef('clientSecret', 'name', v),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _TextField(
                        value: clientSecretRefMap['key'] is String
                            ? clientSecretRefMap['key'] as String
                            : '',
                        label: 'Key',
                        hint: 'client-secret',
                        errorText: errors['authSecretRef.clientSecret.key'],
                        onChanged: (v) =>
                            _patchAuthSecretRef('clientSecret', 'key', v),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),

        if (activeAuthType == 'WorkloadIdentity')
          _AuthBox(
            title: 'Workload Identity',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _TextField(
                  value: props.getString('tenantId'),
                  label: 'Tenant ID',
                  hint: 'xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx',
                  helper: 'Azure AD tenant ID (Directory ID).',
                  errorText: errors['tenantId'],
                  onChanged: (v) => props.patchTop('tenantId', v),
                ),
                const SizedBox(height: 12),
                _TextField(
                  value: saName,
                  label: 'Service account name',
                  hint: 'eso-workload-sa',
                  helper:
                      'The K8s ServiceAccount annotated with the Azure federated identity.',
                  errorText: errors['serviceAccountRef.name'],
                  onChanged: _patchSARefName,
                ),
              ],
            ),
          ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Shared layout helpers (private to this file)
// ---------------------------------------------------------------------------

class _AuthTypeChip extends StatelessWidget {
  const _AuthTypeChip(
      {required this.authType, required this.selected, required this.onTap});
  final _AuthType authType;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          border: Border.all(
              color:
                  selected ? colorScheme.primary : colorScheme.outlineVariant,
              width: selected ? 2 : 1),
          borderRadius: BorderRadius.circular(8),
          color: selected
              ? colorScheme.primary.withValues(alpha: 0.08)
              : colorScheme.surface,
        ),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(authType.label,
                      style: TextStyle(
                          fontWeight: FontWeight.w600,
                          color: selected
                              ? colorScheme.primary
                              : colorScheme.onSurface)),
                  const SizedBox(height: 2),
                  Text(authType.description,
                      style: TextStyle(
                          fontSize: 12, color: colorScheme.onSurfaceVariant)),
                ],
              ),
            ),
            if (selected)
              Icon(Icons.check_circle, color: colorScheme.primary, size: 18),
          ],
        ),
      ),
    );
  }
}

class _AuthBox extends StatelessWidget {
  const _AuthBox({required this.title, required this.child});
  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        border: Border.all(
            color: Theme.of(context).colorScheme.outlineVariant),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title,
              style: Theme.of(context)
                  .textTheme
                  .labelLarge
                  ?.copyWith(fontWeight: FontWeight.w600)),
          const SizedBox(height: 12),
          child,
        ],
      ),
    );
  }
}

class _TextField extends StatefulWidget {
  const _TextField({
    required this.value,
    required this.label,
    required this.onChanged,
    this.hint,
    this.helper,
    this.errorText,
  });

  final String value;
  final String label;
  final String? hint;
  final String? helper;
  final String? errorText;
  final ValueChanged<String> onChanged;

  @override
  State<_TextField> createState() => _TextFieldState();
}

class _TextFieldState extends State<_TextField> {
  late final TextEditingController _ctl =
      TextEditingController(text: widget.value);

  @override
  void didUpdateWidget(covariant _TextField old) {
    super.didUpdateWidget(old);
    if (widget.value != _ctl.text && widget.value != old.value) {
      _ctl.text = widget.value;
      _ctl.selection =
          TextSelection.collapsed(offset: widget.value.length);
    }
  }

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: _ctl,
      decoration: InputDecoration(
        labelText: widget.label,
        hintText: widget.hint,
        helperText: widget.helper,
        helperMaxLines: 2,
        border: const OutlineInputBorder(),
        errorText: widget.errorText,
        isDense: true,
      ),
      onChanged: widget.onChanged,
    );
  }
}
