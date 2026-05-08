// Doppler provider form for SecretStoreWizard.
//
// Wire format written into providerSpec:
//   {
//     project: string,         // required
//     config: string,          // required
//     auth: {
//       secretRef: {           // Service Token auth
//         dopplerToken: { name: string, key: string }
//       }
//       | oidcConfig: {        // OIDC / Workload Identity auth
//           identity: string,
//           serviceAccountRef: { name: string }
//         }
//     }
//   }
//
// Backend validator: backend/internal/wizard/secretstore_doppler.go
// Web ground-truth: frontend/components/wizard/secretstore/DopplerForm.tsx
//
// Auth default: secretRef (Service Token).
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

enum _DopplerAuthMethod { secretRef, oidcConfig }

_DopplerAuthMethod _detectMethod(Map<String, dynamic> spec) {
  final auth = spec['auth'];
  if (auth is! Map<String, dynamic>) return _DopplerAuthMethod.secretRef;
  if (auth.containsKey('secretRef')) return _DopplerAuthMethod.secretRef;
  if (auth.containsKey('oidcConfig')) return _DopplerAuthMethod.oidcConfig;
  return _DopplerAuthMethod.secretRef;
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

Widget dopplerProviderForm(ProviderFormProps props) =>
    _DopplerProviderForm(props: props);

// ---------------------------------------------------------------------------
// Root stateful widget
// ---------------------------------------------------------------------------

class _DopplerProviderForm extends StatefulWidget {
  const _DopplerProviderForm({required this.props});
  final ProviderFormProps props;

  @override
  State<_DopplerProviderForm> createState() => _DopplerProviderFormState();
}

class _DopplerProviderFormState extends State<_DopplerProviderForm> {
  late _DopplerAuthMethod _method;

  @override
  void initState() {
    super.initState();
    _method = _detectMethod(widget.props.spec);
  }

  void _setMethod(_DopplerAuthMethod m) {
    if (_method == m) return;
    setState(() => _method = m);
    final spec = widget.props.spec;
    final next = Map<String, dynamic>.from(spec);
    if (m == _DopplerAuthMethod.secretRef) {
      next['auth'] = {
        'secretRef': {'dopplerToken': <String, dynamic>{}},
      };
    } else {
      next['auth'] = {
        'oidcConfig': {'serviceAccountRef': <String, dynamic>{}},
      };
    }
    widget.props.onUpdateSpec(next);
  }

  void _patchTokenRef(Map<String, dynamic> patch) {
    final spec = widget.props.spec;
    final auth = spec['auth'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(spec['auth'] as Map<String, dynamic>)
        : <String, dynamic>{};
    final sr = auth['secretRef'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(auth['secretRef'] as Map<String, dynamic>)
        : <String, dynamic>{};
    final existing = sr['dopplerToken'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(
            sr['dopplerToken'] as Map<String, dynamic>)
        : <String, dynamic>{};
    existing.addAll(patch);
    sr['dopplerToken'] = existing;
    auth['secretRef'] = sr;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  void _patchOIDC(Map<String, dynamic> patch) {
    final spec = widget.props.spec;
    final auth = spec['auth'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(spec['auth'] as Map<String, dynamic>)
        : <String, dynamic>{};
    final oidc = auth['oidcConfig'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(
            auth['oidcConfig'] as Map<String, dynamic>)
        : <String, dynamic>{};
    oidc.addAll(patch);
    auth['oidcConfig'] = oidc;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  Map<String, dynamic> _authMethodBlock() {
    final auth = widget.props.spec['auth'];
    if (auth is Map<String, dynamic>) {
      final key = _method == _DopplerAuthMethod.secretRef ? 'secretRef' : 'oidcConfig';
      final block = auth[key];
      if (block is Map<String, dynamic>) return block;
    }
    return const <String, dynamic>{};
  }

  @override
  Widget build(BuildContext context) {
    final props = widget.props;
    final errors = props.errors;
    final theme = Theme.of(context);

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
            'Configure the Doppler provider connection. Doppler credentials must '
            'already exist as Kubernetes Secrets in this namespace (for service '
            'token) or be available via Workload Identity (for OIDC) — this '
            'wizard only references them and never stores credentials directly.',
            style: theme.textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: 16),

        // Top-level fields
        _ResyncTextField(
          value: props.getString('project'),
          label: 'Project *',
          hint: 'my-project',
          helper: 'The Doppler project name.',
          error: errors['project'],
          onChanged: (v) => props.patchTop('project', v),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: props.getString('config'),
          label: 'Config *',
          hint: 'prd',
          helper: 'The Doppler config (environment) within the project.',
          error: errors['config'],
          onChanged: (v) => props.patchTop('config', v),
        ),
        const SizedBox(height: 20),

        // Auth method picker
        const WizardSectionHeader(
          'Authentication method',
          subtitle:
              'Service Token is recommended for most clusters. OIDC requires Workload Identity setup.',
        ),
        const SizedBox(height: 8),
        _AuthMethodPicker(
          selected: _method,
          onSelect: _setMethod,
          error: errors['auth'],
        ),
        const SizedBox(height: 16),

        // Auth-method-specific fields
        if (_method == _DopplerAuthMethod.secretRef)
          _ServiceTokenFields(
            block: _authMethodBlock(),
            errors: errors,
            onPatchRef: _patchTokenRef,
          )
        else
          _OIDCFields(
            block: _authMethodBlock(),
            errors: errors,
            onPatch: _patchOIDC,
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

  final _DopplerAuthMethod selected;
  final ValueChanged<_DopplerAuthMethod> onSelect;
  final String? error;

  static const _methods = [
    (
      method: _DopplerAuthMethod.secretRef,
      label: 'Service Token',
      description:
          'A Doppler service token stored in a Kubernetes Secret. Recommended for most clusters.',
    ),
    (
      method: _DopplerAuthMethod.oidcConfig,
      label: 'OIDC (Workload Identity)',
      description:
          'Authenticate via Kubernetes ServiceAccount tokens — no long-lived secrets required.',
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
          Text(
            error!,
            style: TextStyle(color: theme.colorScheme.error, fontSize: 12),
          ),
        ],
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Service Token fields (auth.secretRef.dopplerToken)
// ---------------------------------------------------------------------------

class _ServiceTokenFields extends StatelessWidget {
  const _ServiceTokenFields({
    required this.block,
    required this.errors,
    required this.onPatchRef,
  });

  final Map<String, dynamic> block;
  final Map<String, String> errors;
  final ValueChanged<Map<String, dynamic>> onPatchRef;

  Map<String, dynamic> _tokenRef() {
    final v = block['dopplerToken'];
    return v is Map<String, dynamic> ? v : const <String, dynamic>{};
  }

  @override
  Widget build(BuildContext context) {
    final ref = _tokenRef();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader('Service token Secret reference'),
        const SizedBox(height: 4),
        Text(
          'The key defaults to "dopplerToken" in ESO when omitted, but an '
          'explicit value is required here to produce unambiguous YAML.',
          style: Theme.of(context).textTheme.bodySmall,
        ),
        const SizedBox(height: 8),
        _ResyncTextField(
          value: ref['name'] is String ? ref['name'] as String : '',
          label: 'Secret name *',
          hint: 'doppler-token',
          error: errors['auth.secretRef.dopplerToken.name'],
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
          hint: 'serviceToken',
          error: errors['auth.secretRef.dopplerToken.key'],
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
// OIDC / Workload Identity fields (auth.oidcConfig)
// ---------------------------------------------------------------------------

class _OIDCFields extends StatelessWidget {
  const _OIDCFields({
    required this.block,
    required this.errors,
    required this.onPatch,
  });

  final Map<String, dynamic> block;
  final Map<String, String> errors;
  final ValueChanged<Map<String, dynamic>> onPatch;

  String _saName() {
    final saRef = block['serviceAccountRef'];
    if (saRef is Map<String, dynamic>) {
      final n = saRef['name'];
      return n is String ? n : '';
    }
    return '';
  }

  void _patchSARef(String name) {
    final existing = block['serviceAccountRef'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(
            block['serviceAccountRef'] as Map<String, dynamic>)
        : <String, dynamic>{};
    if (name.isEmpty) {
      existing.remove('name');
    } else {
      existing['name'] = name;
    }
    onPatch({...block, 'serviceAccountRef': existing});
  }

  @override
  Widget build(BuildContext context) {
    final errors = this.errors;
    final identity =
        block['identity'] is String ? block['identity'] as String : '';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader('OIDC / Workload Identity auth'),
        const SizedBox(height: 8),
        _ResyncTextField(
          value: identity,
          label: 'Doppler Identity ID *',
          hint: 'abc123...',
          helper:
              'The Doppler Service Account Identity ID configured for OIDC.',
          error: errors['auth.oidcConfig.identity'],
          onChanged: (v) {
            final next = Map<String, dynamic>.from(block);
            if (v.isEmpty) {
              next.remove('identity');
            } else {
              next['identity'] = v;
            }
            onPatch(next);
          },
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: _saName(),
          label: 'ServiceAccount name *',
          hint: 'my-app',
          helper:
              'Kubernetes ServiceAccount whose token is exchanged for a Doppler credential.',
          error: errors['auth.oidcConfig.serviceAccountRef.name'] ??
              errors['auth.oidcConfig.serviceAccountRef'],
          onChanged: _patchSARef,
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
