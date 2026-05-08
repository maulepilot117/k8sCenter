// Vault provider form for the SecretStore wizard. Stateless dumb widget that
// reads from and emits into ProviderFormProps.spec — no riverpod, no dio.
//
// Wire format (spec.provider.vault shape mirrored from ESO):
//   {
//     server: String,             // required, https://
//     path?: String,              // KV mount path
//     version?: String,           // "v1" | "v2" (default v2)
//     namespace?: String,         // Vault Enterprise namespace only
//     auth: {                     // exactly one of:
//       token:      { tokenSecretRef:    { name, key } }
//       kubernetes: { mountPath, role }
//       appRole:    { path, roleId, secretRef: { name, key } }
//       jwt:        { path, role?, secretRef: { name, key } }
//       cert:       { clientCert: { name, key }, secretRef: { name, key } }
//     }
//   }
//
// Backend validator: backend/internal/wizard/secretstore_vault.go
// Web ground-truth:  frontend/components/wizard/secretstore/VaultForm.tsx
//
// Switching auth method calls onUpdateSpec({...spec, auth: { [method]: emptyBlock }})
// — mirrors VaultForm.tsx setMethod exactly so stale fields don't leak.
//
// TextField cursor-stability pattern: every text input that can be updated
// externally (e.g. when the parent resets the spec) uses a StatefulWidget with
// a TextEditingController and didUpdateWidget resync — the same pattern as
// _AcmeServerField in issuer_wizard_screen.dart.

import 'package:flutter/material.dart';

import 'provider_form.dart';
import '../../../widgets/section_header.dart';

// ---------------------------------------------------------------------------
// Auth method descriptor
// ---------------------------------------------------------------------------

const _kAuthMethods = [
  _AuthMethod('token', 'Token', 'A Vault token loaded from a Kubernetes Secret.'),
  _AuthMethod('kubernetes', 'Kubernetes',
      "Vault's Kubernetes auth method using the pod's service account JWT."),
  _AuthMethod('appRole', 'AppRole',
      'RoleID + SecretID pair, with the SecretID stored in a Secret.'),
  _AuthMethod('jwt', 'JWT / OIDC',
      'JWT or OIDC token, either from a Secret or a service account.'),
  _AuthMethod('cert', 'TLS Cert',
      'Mutual-TLS authentication using a client certificate.'),
];

class _AuthMethod {
  const _AuthMethod(this.id, this.label, this.description);
  final String id;
  final String label;
  final String description;
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/// Top-level builder for the Vault provider form. Exported as
/// `ProviderFormBuilder` — the wizard dispatcher holds a reference to this
/// function, not to a class.
Widget vaultProviderForm(ProviderFormProps props) =>
    _VaultProviderForm(props: props);

// ---------------------------------------------------------------------------
// Root stateful widget
// ---------------------------------------------------------------------------

class _VaultProviderForm extends StatefulWidget {
  const _VaultProviderForm({required this.props});
  final ProviderFormProps props;

  @override
  State<_VaultProviderForm> createState() => _VaultProviderFormState();
}

class _VaultProviderFormState extends State<_VaultProviderForm> {
  // ---------------------------------------------------------------------------
  // Detect the active auth method from the spec. Mirrors detectMethod() in
  // VaultForm.tsx — iterates the ordered list so picking is deterministic.
  // ---------------------------------------------------------------------------
  String _detectMethod(Map<String, dynamic> spec) {
    final auth = spec['auth'];
    if (auth is! Map<String, dynamic>) return 'token';
    for (final m in _kAuthMethods) {
      if (auth.containsKey(m.id)) return m.id;
    }
    return 'token';
  }

  // Empty initial blocks for each method, mirroring emptyMethodSpec in VaultForm.tsx.
  Map<String, dynamic> _emptyBlock(String method) {
    switch (method) {
      case 'token':
        return <String, dynamic>{
          'tokenSecretRef': <String, dynamic>{}
        };
      case 'kubernetes':
        return <String, dynamic>{};
      case 'appRole':
        return <String, dynamic>{'secretRef': <String, dynamic>{}};
      case 'jwt':
        return <String, dynamic>{'secretRef': <String, dynamic>{}};
      case 'cert':
        return <String, dynamic>{
          'clientCert': <String, dynamic>{},
          'secretRef': <String, dynamic>{}
        };
      default:
        return <String, dynamic>{};
    }
  }

  void _setMethod(String method) {
    final spec = widget.props.spec;
    if (_detectMethod(spec) == method) return;
    // Clear auth slate, preserve top-level fields (server/path/version/namespace).
    final next = Map<String, dynamic>.from(spec);
    next['auth'] = <String, dynamic>{method: _emptyBlock(method)};
    widget.props.onUpdateSpec(next);
  }

  /// Patch a single field inside auth[method][...].
  void _patchAuthField(String method, String field, String value) {
    final spec = widget.props.spec;
    final auth = Map<String, dynamic>.from(
        spec['auth'] is Map<String, dynamic> ? spec['auth'] as Map<String, dynamic> : <String, dynamic>{});
    final block = Map<String, dynamic>.from(
        auth[method] is Map<String, dynamic> ? auth[method] as Map<String, dynamic> : <String, dynamic>{});
    if (value.isEmpty) {
      block.remove(field);
    } else {
      block[field] = value;
    }
    auth[method] = block;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  /// Patch a nested SecretRef sub-map inside auth[method][refField].
  void _patchSecretRef(
      String method, String refField, String subKey, String value) {
    final spec = widget.props.spec;
    final auth = Map<String, dynamic>.from(
        spec['auth'] is Map<String, dynamic> ? spec['auth'] as Map<String, dynamic> : <String, dynamic>{});
    final block = Map<String, dynamic>.from(
        auth[method] is Map<String, dynamic> ? auth[method] as Map<String, dynamic> : <String, dynamic>{});
    final ref = Map<String, dynamic>.from(
        block[refField] is Map<String, dynamic> ? block[refField] as Map<String, dynamic> : <String, dynamic>{});
    if (value.isEmpty) {
      ref.remove(subKey);
    } else {
      ref[subKey] = value;
    }
    block[refField] = ref;
    auth[method] = block;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  // ---------------------------------------------------------------------------
  // Helpers for reading auth sub-blocks
  // ---------------------------------------------------------------------------
  Map<String, dynamic> _authBlock(String method) {
    final auth = widget.props.spec['auth'];
    if (auth is! Map<String, dynamic>) return <String, dynamic>{};
    final block = auth[method];
    return block is Map<String, dynamic> ? block : <String, dynamic>{};
  }

  Map<String, dynamic> _secretRef(String method, String refField) {
    final block = _authBlock(method);
    final ref = block[refField];
    return ref is Map<String, dynamic> ? ref : <String, dynamic>{};
  }

  @override
  Widget build(BuildContext context) {
    final props = widget.props;
    final spec = props.spec;
    final errors = props.errors;
    final activeMethod = _detectMethod(spec);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // Info banner
        Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            border:
                Border.all(color: Theme.of(context).colorScheme.outlineVariant),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Text(
            'Configure the Vault server connection and authentication method. '
            'Credentials must already exist as Kubernetes Secrets in this namespace; '
            'this wizard only references them.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: 16),

        // --- Top-level fields ---
        const WizardSectionHeader('Connection'),
        const SizedBox(height: 8),
        _TextField(
          value: props.getString('server'),
          label: 'Server URL',
          hint: 'https://vault.example.com:8200',
          helper: 'Must use https. Private and in-cluster addresses are accepted.',
          errorText: errors['server'],
          onChanged: (v) => props.patchTop('server', v),
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: _TextField(
                value: props.getString('path'),
                label: 'Mount path (optional)',
                hint: 'secret',
                helper: 'KV mount name. Leave blank for ESO default.',
                errorText: errors['path'],
                onChanged: (v) => props.patchTop('path', v),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _VersionDropdown(
                value: spec['version'] is String
                    ? spec['version'] as String
                    : 'v2',
                errorText: errors['version'],
                onChanged: (v) => props.patchTop('version', v),
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        _TextField(
          value: props.getString('namespace'),
          label: 'Vault namespace (Enterprise, optional)',
          hint: 'admin/dev',
          helper: 'Vault Enterprise namespaces only. Leave blank for OSS.',
          errorText: errors['namespace'],
          onChanged: (v) => props.patchTop('namespace', v),
        ),
        const SizedBox(height: 20),

        // --- Auth method picker ---
        const WizardSectionHeader(
          'Authentication method',
          subtitle: 'Select one — switching resets the auth fields.',
        ),
        const SizedBox(height: 8),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final m in _kAuthMethods)
              _AuthChip(
                method: m,
                selected: activeMethod == m.id,
                onTap: () => _setMethod(m.id),
              ),
          ],
        ),
        if (errors['auth'] != null) ...[
          const SizedBox(height: 6),
          Text(errors['auth']!,
              style: TextStyle(
                  color: Theme.of(context).colorScheme.error, fontSize: 12)),
        ],
        const SizedBox(height: 16),

        // --- Per-method fields ---
        if (activeMethod == 'token')
          _TokenFields(
            ref: _secretRef('token', 'tokenSecretRef'),
            errors: errors,
            onPatch: (k, v) =>
                _patchSecretRef('token', 'tokenSecretRef', k, v),
          ),
        if (activeMethod == 'kubernetes')
          _KubernetesFields(
            block: _authBlock('kubernetes'),
            errors: errors,
            onPatch: (k, v) => _patchAuthField('kubernetes', k, v),
          ),
        if (activeMethod == 'appRole')
          _AppRoleFields(
            block: _authBlock('appRole'),
            secretRef: _secretRef('appRole', 'secretRef'),
            errors: errors,
            onPatchField: (k, v) => _patchAuthField('appRole', k, v),
            onPatchRef: (k, v) =>
                _patchSecretRef('appRole', 'secretRef', k, v),
          ),
        if (activeMethod == 'jwt')
          _JWTFields(
            block: _authBlock('jwt'),
            secretRef: _secretRef('jwt', 'secretRef'),
            errors: errors,
            onPatchField: (k, v) => _patchAuthField('jwt', k, v),
            onPatchRef: (k, v) =>
                _patchSecretRef('jwt', 'secretRef', k, v),
          ),
        if (activeMethod == 'cert')
          _CertFields(
            clientCert: _secretRef('cert', 'clientCert'),
            secretRef: _secretRef('cert', 'secretRef'),
            errors: errors,
            onPatchClientCert: (k, v) =>
                _patchSecretRef('cert', 'clientCert', k, v),
            onPatchSecretRef: (k, v) =>
                _patchSecretRef('cert', 'secretRef', k, v),
          ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Auth method chip
// ---------------------------------------------------------------------------

class _AuthChip extends StatelessWidget {
  const _AuthChip(
      {required this.method, required this.selected, required this.onTap});
  final _AuthMethod method;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          border: Border.all(
              color: selected ? colorScheme.primary : colorScheme.outlineVariant,
              width: selected ? 2 : 1),
          borderRadius: BorderRadius.circular(8),
          color: selected
              ? colorScheme.primary.withValues(alpha: 0.08)
              : colorScheme.surface,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(method.label,
                style: TextStyle(
                    fontWeight: FontWeight.w600,
                    color: selected
                        ? colorScheme.primary
                        : colorScheme.onSurface)),
            const SizedBox(height: 2),
            SizedBox(
              width: 120,
              child: Text(method.description,
                  style: TextStyle(
                      fontSize: 11,
                      color: colorScheme.onSurfaceVariant)),
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Per-method field widgets
// ---------------------------------------------------------------------------

class _TokenFields extends StatelessWidget {
  const _TokenFields(
      {required this.ref, required this.errors, required this.onPatch});
  final Map<String, dynamic> ref;
  final Map<String, String> errors;
  final void Function(String key, String value) onPatch;

  @override
  Widget build(BuildContext context) {
    return _AuthBox(
      title: 'Token Secret reference',
      child: Row(
        children: [
          Expanded(
            child: _TextField(
              value: ref['name'] is String ? ref['name'] as String : '',
              label: 'Secret name',
              hint: 'vault-token',
              errorText: errors['auth.token.tokenSecretRef.name'],
              onChanged: (v) => onPatch('name', v),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: _TextField(
              value: ref['key'] is String ? ref['key'] as String : '',
              label: 'Key',
              hint: 'token',
              errorText: errors['auth.token.tokenSecretRef.key'],
              onChanged: (v) => onPatch('key', v),
            ),
          ),
        ],
      ),
    );
  }
}

class _KubernetesFields extends StatelessWidget {
  const _KubernetesFields(
      {required this.block, required this.errors, required this.onPatch});
  final Map<String, dynamic> block;
  final Map<String, String> errors;
  final void Function(String key, String value) onPatch;

  @override
  Widget build(BuildContext context) {
    return _AuthBox(
      title: 'Kubernetes auth',
      child: Row(
        children: [
          Expanded(
            child: _TextField(
              value: block['mountPath'] is String
                  ? block['mountPath'] as String
                  : '',
              label: 'Mount path',
              hint: 'kubernetes',
              helper: 'The Vault auth path where Kubernetes auth is enabled.',
              errorText: errors['auth.kubernetes.mountPath'],
              onChanged: (v) => onPatch('mountPath', v),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: _TextField(
              value:
                  block['role'] is String ? block['role'] as String : '',
              label: 'Role',
              hint: 'my-app',
              helper: 'Vault role bound to this service account.',
              errorText: errors['auth.kubernetes.role'],
              onChanged: (v) => onPatch('role', v),
            ),
          ),
        ],
      ),
    );
  }
}

class _AppRoleFields extends StatelessWidget {
  const _AppRoleFields({
    required this.block,
    required this.secretRef,
    required this.errors,
    required this.onPatchField,
    required this.onPatchRef,
  });
  final Map<String, dynamic> block;
  final Map<String, dynamic> secretRef;
  final Map<String, String> errors;
  final void Function(String key, String value) onPatchField;
  final void Function(String key, String value) onPatchRef;

  @override
  Widget build(BuildContext context) {
    return _AuthBox(
      title: 'AppRole auth',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: _TextField(
                  value: block['path'] is String ? block['path'] as String : '',
                  label: 'Auth path',
                  hint: 'approle',
                  errorText: errors['auth.appRole.path'],
                  onChanged: (v) => onPatchField('path', v),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: _TextField(
                  value: block['roleId'] is String
                      ? block['roleId'] as String
                      : '',
                  label: 'Role ID',
                  hint: 'abc-123-…',
                  helper:
                      'Literal RoleID from `vault read auth/approle/role/<name>/role-id`.',
                  errorText: errors['auth.appRole.roleId'],
                  onChanged: (v) => onPatchField('roleId', v),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Text('SecretID Secret reference',
              style: Theme.of(context)
                  .textTheme
                  .labelMedium
                  ?.copyWith(fontWeight: FontWeight.w600)),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: _TextField(
                  value: secretRef['name'] is String
                      ? secretRef['name'] as String
                      : '',
                  label: 'Secret name',
                  hint: 'approle-secret',
                  errorText: errors['auth.appRole.secretRef.name'],
                  onChanged: (v) => onPatchRef('name', v),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: _TextField(
                  value: secretRef['key'] is String
                      ? secretRef['key'] as String
                      : '',
                  label: 'Key',
                  hint: 'secret-id',
                  errorText: errors['auth.appRole.secretRef.key'],
                  onChanged: (v) => onPatchRef('key', v),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _JWTFields extends StatelessWidget {
  const _JWTFields({
    required this.block,
    required this.secretRef,
    required this.errors,
    required this.onPatchField,
    required this.onPatchRef,
  });
  final Map<String, dynamic> block;
  final Map<String, dynamic> secretRef;
  final Map<String, String> errors;
  final void Function(String key, String value) onPatchField;
  final void Function(String key, String value) onPatchRef;

  @override
  Widget build(BuildContext context) {
    return _AuthBox(
      title: 'JWT / OIDC auth',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: _TextField(
                  value: block['path'] is String ? block['path'] as String : '',
                  label: 'Auth path',
                  hint: 'jwt',
                  errorText: errors['auth.jwt.path'],
                  onChanged: (v) => onPatchField('path', v),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: _TextField(
                  value:
                      block['role'] is String ? block['role'] as String : '',
                  label: 'Role (optional)',
                  hint: 'my-role',
                  onChanged: (v) => onPatchField('role', v),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Text('JWT Secret reference',
              style: Theme.of(context)
                  .textTheme
                  .labelMedium
                  ?.copyWith(fontWeight: FontWeight.w600)),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: _TextField(
                  value: secretRef['name'] is String
                      ? secretRef['name'] as String
                      : '',
                  label: 'Secret name',
                  hint: 'jwt-token',
                  errorText: errors['auth.jwt.secretRef.name'],
                  onChanged: (v) => onPatchRef('name', v),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: _TextField(
                  value: secretRef['key'] is String
                      ? secretRef['key'] as String
                      : '',
                  label: 'Key',
                  hint: 'jwt',
                  errorText: errors['auth.jwt.secretRef.key'],
                  onChanged: (v) => onPatchRef('key', v),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _CertFields extends StatelessWidget {
  const _CertFields({
    required this.clientCert,
    required this.secretRef,
    required this.errors,
    required this.onPatchClientCert,
    required this.onPatchSecretRef,
  });
  final Map<String, dynamic> clientCert;
  final Map<String, dynamic> secretRef;
  final Map<String, String> errors;
  final void Function(String key, String value) onPatchClientCert;
  final void Function(String key, String value) onPatchSecretRef;

  @override
  Widget build(BuildContext context) {
    return _AuthBox(
      title: 'TLS Cert auth',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Client certificate',
              style: Theme.of(context)
                  .textTheme
                  .labelMedium
                  ?.copyWith(fontWeight: FontWeight.w600)),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: _TextField(
                  value: clientCert['name'] is String
                      ? clientCert['name'] as String
                      : '',
                  label: 'Secret name',
                  hint: 'vault-client-cert',
                  errorText: errors['auth.cert.clientCert.name'],
                  onChanged: (v) => onPatchClientCert('name', v),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: _TextField(
                  value: clientCert['key'] is String
                      ? clientCert['key'] as String
                      : '',
                  label: 'Key',
                  hint: 'tls.crt',
                  errorText: errors['auth.cert.clientCert.key'],
                  onChanged: (v) => onPatchClientCert('key', v),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Text('Client key',
              style: Theme.of(context)
                  .textTheme
                  .labelMedium
                  ?.copyWith(fontWeight: FontWeight.w600)),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: _TextField(
                  value: secretRef['name'] is String
                      ? secretRef['name'] as String
                      : '',
                  label: 'Secret name',
                  hint: 'vault-client-key',
                  errorText: errors['auth.cert.secretRef.name'],
                  onChanged: (v) => onPatchSecretRef('name', v),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: _TextField(
                  value: secretRef['key'] is String
                      ? secretRef['key'] as String
                      : '',
                  label: 'Key',
                  hint: 'tls.key',
                  errorText: errors['auth.cert.secretRef.key'],
                  onChanged: (v) => onPatchSecretRef('key', v),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Shared layout helpers (private to this file)
// ---------------------------------------------------------------------------

/// A bordered box used to group auth-method-specific fields.
class _AuthBox extends StatelessWidget {
  const _AuthBox({required this.title, required this.child});
  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        border: Border.all(color: colorScheme.outlineVariant),
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

/// KV version dropdown (v1 / v2).
class _VersionDropdown extends StatelessWidget {
  const _VersionDropdown(
      {required this.value,
      required this.onChanged,
      this.errorText});
  final String value;
  final ValueChanged<String> onChanged;
  final String? errorText;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('KV version',
            style: Theme.of(context).textTheme.labelMedium),
        const SizedBox(height: 4),
        DropdownButtonFormField<String>(
          initialValue: value.isEmpty ? 'v2' : value,
          decoration: InputDecoration(
            border: const OutlineInputBorder(),
            isDense: true,
            errorText: errorText,
          ),
          items: const [
            DropdownMenuItem(value: 'v2', child: Text('v2 (recommended)')),
            DropdownMenuItem(value: 'v1', child: Text('v1')),
          ],
          onChanged: (v) {
            if (v != null) onChanged(v);
          },
        ),
      ],
    );
  }
}

/// Cursor-stable text field. Uses a StatefulWidget with TextEditingController
/// so didUpdateWidget can resync only on external value changes (e.g. a spec
/// reset) without jumping the cursor during in-progress typing.
///
/// Pattern mirrors _AcmeServerField in issuer_wizard_screen.dart.
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
    // Resync only when the parent pushed a new value that differs from what
    // the controller currently shows — and only when it also differs from the
    // previous prop value (i.e. it's not just an echo of the user's own
    // keystroke). This matches the _AcmeServerField pattern exactly.
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
