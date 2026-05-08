// Kubernetes (cross-namespace / cross-cluster) provider form for SecretStoreWizard.
//
// Wire format written into providerSpec:
//   {
//     remoteNamespace?: string,    // optional (ESO defaults to "default")
//     server?: {
//       url?: string,              // optional; must use https when set
//       caBundle?: string,         // optional; base64-encoded CA bundle
//     },
//     auth: {
//       serviceAccount: { name: string, audiences?: string[] }
//       | token: { bearerToken: { name: string, key: string } }
//       | cert: {
//           clientCert: { name: string, key: string },
//           clientKey:  { name: string, key: string },
//         }
//     }
//   }
//
// Backend validator: backend/internal/wizard/secretstore_kubernetes.go
// Web ground-truth: frontend/components/wizard/secretstore/KubernetesForm.tsx
//
// Auth default: serviceAccount.
// Switching auth method clears the auth slate so stale fields don't leak into
// the YAML preview.
//
// All text inputs are stateful (_ResyncTextField) to avoid cursor-jumping.

import 'package:flutter/material.dart';

import 'provider_form.dart';
import '../../../widgets/section_header.dart';

// ---------------------------------------------------------------------------
// Auth method enum
// ---------------------------------------------------------------------------

enum _K8sAuthMethod { serviceAccount, token, cert }

_K8sAuthMethod _detectMethod(Map<String, dynamic> spec) {
  final auth = spec['auth'];
  if (auth is! Map<String, dynamic>) return _K8sAuthMethod.serviceAccount;
  if (auth.containsKey('serviceAccount')) return _K8sAuthMethod.serviceAccount;
  if (auth.containsKey('token')) return _K8sAuthMethod.token;
  if (auth.containsKey('cert')) return _K8sAuthMethod.cert;
  return _K8sAuthMethod.serviceAccount;
}

Map<String, dynamic> _emptyAuthBlock(_K8sAuthMethod m) {
  switch (m) {
    case _K8sAuthMethod.serviceAccount:
      return {'serviceAccount': <String, dynamic>{}};
    case _K8sAuthMethod.token:
      return {
        'token': {'bearerToken': <String, dynamic>{}},
      };
    case _K8sAuthMethod.cert:
      return {
        'cert': {
          'clientCert': <String, dynamic>{},
          'clientKey': <String, dynamic>{},
        },
      };
  }
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

Widget kubernetesProviderForm(ProviderFormProps props) =>
    _KubernetesProviderForm(props: props);

// ---------------------------------------------------------------------------
// Root stateful widget
// ---------------------------------------------------------------------------

class _KubernetesProviderForm extends StatefulWidget {
  const _KubernetesProviderForm({required this.props});
  final ProviderFormProps props;

  @override
  State<_KubernetesProviderForm> createState() =>
      _KubernetesProviderFormState();
}

class _KubernetesProviderFormState extends State<_KubernetesProviderForm> {
  late _K8sAuthMethod _method;
  bool _showAdvanced = false;

  @override
  void initState() {
    super.initState();
    _method = _detectMethod(widget.props.spec);
  }

  void _setMethod(_K8sAuthMethod m) {
    if (_method == m) return;
    setState(() => _method = m);
    final spec = widget.props.spec;
    final next = Map<String, dynamic>.from(spec);
    next['auth'] = _emptyAuthBlock(m);
    widget.props.onUpdateSpec(next);
  }

  // Patch the server sub-block; prune empty values; remove when entirely empty.
  void _patchServer(String key, String value) {
    final spec = widget.props.spec;
    final srv = spec['server'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(spec['server'] as Map<String, dynamic>)
        : <String, dynamic>{};
    if (value.isEmpty) {
      srv.remove(key);
    } else {
      srv[key] = value;
    }
    final next = Map<String, dynamic>.from(spec);
    if (srv.isEmpty) {
      next.remove('server');
    } else {
      next['server'] = srv;
    }
    widget.props.onUpdateSpec(next);
  }

  // Patch a field inside auth.<method>.
  void _patchAuthBlock(_K8sAuthMethod method, Map<String, dynamic> patch) {
    final spec = widget.props.spec;
    final auth = spec['auth'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(spec['auth'] as Map<String, dynamic>)
        : <String, dynamic>{};
    final block = auth[_methodKey(method)] is Map<String, dynamic>
        ? Map<String, dynamic>.from(
            auth[_methodKey(method)] as Map<String, dynamic>)
        : <String, dynamic>{};
    block.addAll(patch);
    auth[_methodKey(method)] = block;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  // Patch a nested SecretRef inside auth.<method>.<refField>.
  void _patchSecretRef(
      _K8sAuthMethod method, String refField, Map<String, dynamic> patch) {
    final spec = widget.props.spec;
    final auth = spec['auth'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(spec['auth'] as Map<String, dynamic>)
        : <String, dynamic>{};
    final block = auth[_methodKey(method)] is Map<String, dynamic>
        ? Map<String, dynamic>.from(
            auth[_methodKey(method)] as Map<String, dynamic>)
        : <String, dynamic>{};
    final existing = block[refField] is Map<String, dynamic>
        ? Map<String, dynamic>.from(block[refField] as Map<String, dynamic>)
        : <String, dynamic>{};
    existing.addAll(patch);
    block[refField] = existing;
    auth[_methodKey(method)] = block;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  static String _methodKey(_K8sAuthMethod m) {
    switch (m) {
      case _K8sAuthMethod.serviceAccount:
        return 'serviceAccount';
      case _K8sAuthMethod.token:
        return 'token';
      case _K8sAuthMethod.cert:
        return 'cert';
    }
  }

  Map<String, dynamic> _authBlock() {
    final auth = widget.props.spec['auth'];
    if (auth is Map<String, dynamic>) {
      final block = auth[_methodKey(_method)];
      if (block is Map<String, dynamic>) return block;
    }
    return const <String, dynamic>{};
  }

  Map<String, dynamic> _serverBlock() {
    final srv = widget.props.spec['server'];
    return srv is Map<String, dynamic> ? srv : const <String, dynamic>{};
  }

  @override
  Widget build(BuildContext context) {
    final props = widget.props;
    final errors = props.errors;
    final theme = Theme.of(context);
    final srv = _serverBlock();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // Info banner
        Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            border: Border.all(color: theme.colorScheme.outlineVariant),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Text(
            'The Kubernetes provider reads Secrets from another namespace (or '
            'cluster) via service-account impersonation. Set Remote namespace '
            'to the source namespace, then choose how ESO authenticates.',
            style: theme.textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: 16),

        // Remote namespace
        _ResyncTextField(
          value: props.getString('remoteNamespace'),
          label: 'Remote namespace (optional)',
          hint: 'secrets-ns',
          helper:
              'Namespace in the source cluster where Secrets live. Defaults to "default" in ESO when omitted.',
          error: errors['remoteNamespace'],
          onChanged: (v) => props.patchTop('remoteNamespace', v),
        ),
        const SizedBox(height: 16),

        // Advanced: custom apiserver URL (collapsible)
        InkWell(
          onTap: () => setState(() => _showAdvanced = !_showAdvanced),
          borderRadius: BorderRadius.circular(4),
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Row(
              children: [
                Icon(
                  _showAdvanced ? Icons.expand_less : Icons.expand_more,
                  size: 18,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                const SizedBox(width: 4),
                Text(
                  'Advanced: custom apiserver URL',
                  style: theme.textTheme.bodySmall?.copyWith(
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ],
            ),
          ),
        ),
        if (_showAdvanced) ...[
          const SizedBox(height: 8),
          _ResyncTextField(
            value: (srv['url'] is String ? srv['url'] as String : ''),
            label: 'Apiserver URL (optional)',
            hint: 'https://apiserver.example.com:6443',
            helper:
                'Leave blank to use the in-cluster apiserver. Must use https.',
            error: errors['server.url'],
            onChanged: (v) => _patchServer('url', v),
          ),
          const SizedBox(height: 12),
          _ResyncTextField(
            value: (srv['caBundle'] is String ? srv['caBundle'] as String : ''),
            label: 'CA bundle (base64, optional)',
            hint: 'LS0tLS1CRUdJTi…',
            helper:
                'Base64-encoded CA bundle for the target apiserver. Leave blank to use the cluster default.',
            error: errors['server.caBundle'],
            onChanged: (v) => _patchServer('caBundle', v),
          ),
          const SizedBox(height: 8),
        ],
        const SizedBox(height: 16),

        // Auth method picker
        const WizardSectionHeader(
          'Authentication method',
          subtitle: 'Choose how ESO presents credentials to the target apiserver.',
        ),
        const SizedBox(height: 8),
        _AuthMethodPicker(
          selected: _method,
          onSelect: _setMethod,
          error: errors['auth'],
        ),
        const SizedBox(height: 16),

        // Auth-method-specific fields
        if (_method == _K8sAuthMethod.serviceAccount)
          _ServiceAccountFields(
            block: _authBlock(),
            errors: errors,
            onPatch: (patch) => _patchAuthBlock(_K8sAuthMethod.serviceAccount, patch),
          )
        else if (_method == _K8sAuthMethod.token)
          _TokenFields(
            block: _authBlock(),
            errors: errors,
            onPatchRef: (patch) =>
                _patchSecretRef(_K8sAuthMethod.token, 'bearerToken', patch),
          )
        else
          _CertFields(
            block: _authBlock(),
            errors: errors,
            onPatchClientCert: (patch) =>
                _patchSecretRef(_K8sAuthMethod.cert, 'clientCert', patch),
            onPatchClientKey: (patch) =>
                _patchSecretRef(_K8sAuthMethod.cert, 'clientKey', patch),
          ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Auth method picker
// ---------------------------------------------------------------------------

class _AuthMethodPicker extends StatelessWidget {
  const _AuthMethodPicker({
    required this.selected,
    required this.onSelect,
    this.error,
  });

  final _K8sAuthMethod selected;
  final ValueChanged<_K8sAuthMethod> onSelect;
  final String? error;

  static const _methods = [
    (
      method: _K8sAuthMethod.serviceAccount,
      label: 'ServiceAccount',
      description:
          'Impersonate a named ServiceAccount in the source namespace.',
    ),
    (
      method: _K8sAuthMethod.token,
      label: 'Bearer Token',
      description: 'A static bearer token stored in a local Kubernetes Secret.',
    ),
    (
      method: _K8sAuthMethod.cert,
      label: 'TLS Cert',
      description:
          'Mutual-TLS with a client certificate + key pair from Secrets.',
    ),
  ];

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: _methods
              .map(
                (m) => ChoiceChip(
                  label: Text(m.label),
                  selected: selected == m.method,
                  tooltip: m.description,
                  onSelected: (_) => onSelect(m.method),
                ),
              )
              .toList(),
        ),
        if (error != null) ...[
          const SizedBox(height: 4),
          Text(error!,
              style: TextStyle(
                  color: theme.colorScheme.error, fontSize: 12)),
        ],
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// ServiceAccount auth fields
// ---------------------------------------------------------------------------

class _ServiceAccountFields extends StatefulWidget {
  const _ServiceAccountFields({
    required this.block,
    required this.errors,
    required this.onPatch,
  });

  final Map<String, dynamic> block;
  final Map<String, String> errors;
  final ValueChanged<Map<String, dynamic>> onPatch;

  @override
  State<_ServiceAccountFields> createState() => _ServiceAccountFieldsState();
}

class _ServiceAccountFieldsState extends State<_ServiceAccountFields> {
  late final TextEditingController _nameClt = TextEditingController(
      text: widget.block['name'] is String ? widget.block['name'] as String : '');
  late final TextEditingController _audClt = TextEditingController(
    text: _audiencesToStr(widget.block),
  );

  static String _audiencesToStr(Map<String, dynamic> block) {
    final aud = block['audiences'];
    if (aud is List) return aud.join(', ');
    return '';
  }

  @override
  void didUpdateWidget(covariant _ServiceAccountFields old) {
    super.didUpdateWidget(old);
    final newName =
        widget.block['name'] is String ? widget.block['name'] as String : '';
    if (newName != _nameClt.text && newName != (old.block['name'] ?? '')) {
      _nameClt.text = newName;
      _nameClt.selection =
          TextSelection.collapsed(offset: newName.length);
    }
    final newAud = _audiencesToStr(widget.block);
    final oldAud = _audiencesToStr(old.block);
    if (newAud != _audClt.text && newAud != oldAud) {
      _audClt.text = newAud;
      _audClt.selection =
          TextSelection.collapsed(offset: newAud.length);
    }
  }

  @override
  void dispose() {
    _nameClt.dispose();
    _audClt.dispose();
    super.dispose();
  }

  void _handleAudiencesChange(String raw) {
    final trimmed = raw.trim();
    final next = Map<String, dynamic>.from(widget.block);
    if (trimmed.isEmpty) {
      next.remove('audiences');
    } else {
      next['audiences'] =
          trimmed.split(',').map((s) => s.trim()).where((s) => s.isNotEmpty).toList();
    }
    widget.onPatch(next);
  }

  @override
  Widget build(BuildContext context) {
    final errors = widget.errors;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader('ServiceAccount'),
        const SizedBox(height: 8),
        TextField(
          controller: _nameClt,
          decoration: InputDecoration(
            labelText: 'ServiceAccount name *',
            hintText: 'eso-reader',
            helperText:
                'The SA in the source namespace whose token ESO presents to the apiserver.',
            helperMaxLines: 2,
            border: const OutlineInputBorder(),
            errorText: errors['auth.serviceAccount.name'],
            isDense: true,
          ),
          onChanged: (v) {
            final next = Map<String, dynamic>.from(widget.block);
            if (v.isEmpty) {
              next.remove('name');
            } else {
              next['name'] = v;
            }
            widget.onPatch(next);
          },
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _audClt,
          decoration: InputDecoration(
            labelText: 'Token audiences (optional)',
            hintText: 'https://kubernetes.default.svc',
            helperText:
                'Comma-separated list. Leave blank for the default cluster audience.',
            helperMaxLines: 2,
            border: const OutlineInputBorder(),
            errorText: errors['auth.serviceAccount.audiences'],
            isDense: true,
          ),
          onChanged: _handleAudiencesChange,
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Bearer token auth fields
// ---------------------------------------------------------------------------

class _TokenFields extends StatelessWidget {
  const _TokenFields({
    required this.block,
    required this.errors,
    required this.onPatchRef,
  });

  final Map<String, dynamic> block;
  final Map<String, String> errors;
  final ValueChanged<Map<String, dynamic>> onPatchRef;

  Map<String, dynamic> _tokenRef() {
    final bt = block['bearerToken'];
    return bt is Map<String, dynamic> ? bt : const <String, dynamic>{};
  }

  @override
  Widget build(BuildContext context) {
    final ref = _tokenRef();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader('Bearer token Secret reference'),
        const SizedBox(height: 8),
        _ResyncTextField(
          value: ref['name'] is String ? ref['name'] as String : '',
          label: 'Secret name *',
          hint: 'k8s-token',
          error: errors['auth.token.bearerToken.name'],
          onChanged: (v) => onPatchRef(
            v.isEmpty
                ? (Map<String, dynamic>.from(ref)..remove('name'))
                : {...ref, 'name': v},
          ),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: ref['key'] is String ? ref['key'] as String : '',
          label: 'Key *',
          hint: 'token',
          error: errors['auth.token.bearerToken.key'],
          onChanged: (v) => onPatchRef(
            v.isEmpty
                ? (Map<String, dynamic>.from(ref)..remove('key'))
                : {...ref, 'key': v},
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// TLS client certificate auth fields
// ---------------------------------------------------------------------------

class _CertFields extends StatelessWidget {
  const _CertFields({
    required this.block,
    required this.errors,
    required this.onPatchClientCert,
    required this.onPatchClientKey,
  });

  final Map<String, dynamic> block;
  final Map<String, String> errors;
  final ValueChanged<Map<String, dynamic>> onPatchClientCert;
  final ValueChanged<Map<String, dynamic>> onPatchClientKey;

  Map<String, dynamic> _subRef(String field) {
    final v = block[field];
    return v is Map<String, dynamic> ? v : const <String, dynamic>{};
  }

  @override
  Widget build(BuildContext context) {
    final cc = _subRef('clientCert');
    final ck = _subRef('clientKey');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader('Client certificate'),
        const SizedBox(height: 8),
        _ResyncTextField(
          value: cc['name'] is String ? cc['name'] as String : '',
          label: 'Secret name *',
          hint: 'k8s-client-cert',
          error: errors['auth.cert.clientCert.name'],
          onChanged: (v) => onPatchClientCert(
            v.isEmpty
                ? (Map<String, dynamic>.from(cc)..remove('name'))
                : {...cc, 'name': v},
          ),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: cc['key'] is String ? cc['key'] as String : '',
          label: 'Key *',
          hint: 'tls.crt',
          error: errors['auth.cert.clientCert.key'],
          onChanged: (v) => onPatchClientCert(
            v.isEmpty
                ? (Map<String, dynamic>.from(cc)..remove('key'))
                : {...cc, 'key': v},
          ),
        ),
        const SizedBox(height: 20),
        const WizardSectionHeader('Client key'),
        const SizedBox(height: 8),
        _ResyncTextField(
          value: ck['name'] is String ? ck['name'] as String : '',
          label: 'Secret name *',
          hint: 'k8s-client-key',
          error: errors['auth.cert.clientKey.name'],
          onChanged: (v) => onPatchClientKey(
            v.isEmpty
                ? (Map<String, dynamic>.from(ck)..remove('name'))
                : {...ck, 'name': v},
          ),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: ck['key'] is String ? ck['key'] as String : '',
          label: 'Key *',
          hint: 'tls.key',
          error: errors['auth.cert.clientKey.key'],
          onChanged: (v) => onPatchClientKey(
            v.isEmpty
                ? (Map<String, dynamic>.from(ck)..remove('key'))
                : {...ck, 'key': v},
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Reusable stateful text field
// ---------------------------------------------------------------------------

class _ResyncTextField extends StatefulWidget {
  const _ResyncTextField({
    required this.value,
    required this.label,
    required this.onChanged,
    this.hint,
    this.helper,
    this.error,
  });

  final String value;
  final String label;
  final String? hint;
  final String? helper;
  final String? error;
  final ValueChanged<String> onChanged;

  @override
  State<_ResyncTextField> createState() => _ResyncTextFieldState();
}

class _ResyncTextFieldState extends State<_ResyncTextField> {
  late final TextEditingController _ctl =
      TextEditingController(text: widget.value);

  @override
  void didUpdateWidget(covariant _ResyncTextField old) {
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
        errorText: widget.error,
        isDense: true,
      ),
      onChanged: widget.onChanged,
    );
  }
}
