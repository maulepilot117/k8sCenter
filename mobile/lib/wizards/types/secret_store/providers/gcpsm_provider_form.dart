// GCP Secret Manager provider form for SecretStoreWizard.
//
// Wire format written into providerSpec:
//   {
//     projectID: string,           // required
//     location?: string,           // optional — omitted when blank
//     auth?: {
//       workloadIdentity: {        // GKE Workload Identity
//         serviceAccountRef: { name: string },
//         clusterLocation?: string,
//         clusterName?: string,
//         clusterProjectID?: string,
//       }
//       | secretRef: {             // SA JSON key from a Kubernetes Secret
//         secretAccessKeySecretRef: { name: string, key: string }
//       }
//       // absent = default credentials (GKE metadata server / ADC)
//     }
//   }
//
// Backend validator: backend/internal/wizard/secretstore_gcpsm.go
// Web ground-truth: frontend/components/wizard/secretstore/GCPSMForm.tsx
//
// Auth default: workloadIdentity.
// Switching auth method clears the auth slate so stale fields don't leak into
// the YAML preview.
//
// All text inputs are stateful (_ResyncTextField) so external value changes
// (e.g. switching auth method) update the visible field without jumping the
// cursor during normal typing — mirrors _AcmeServerField in issuer_wizard_screen.dart.

import 'package:flutter/material.dart';

import 'provider_form.dart';
import '../../../widgets/section_header.dart';

// ---------------------------------------------------------------------------
// Auth method enum
// ---------------------------------------------------------------------------

enum _GCPSMAuthMethod { workloadIdentity, secretRef, defaultCreds }

_GCPSMAuthMethod _detectMethod(Map<String, dynamic> spec) {
  final auth = spec['auth'];
  if (auth is! Map<String, dynamic>) return _GCPSMAuthMethod.workloadIdentity;
  if (auth.containsKey('workloadIdentity')) {
    return _GCPSMAuthMethod.workloadIdentity;
  }
  if (auth.containsKey('secretRef')) return _GCPSMAuthMethod.secretRef;
  return _GCPSMAuthMethod.defaultCreds;
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

Widget gcpsmProviderForm(ProviderFormProps props) =>
    _GCPSMProviderForm(props: props);

// ---------------------------------------------------------------------------
// Root stateful widget — owns auth method selection state
// ---------------------------------------------------------------------------

class _GCPSMProviderForm extends StatefulWidget {
  const _GCPSMProviderForm({required this.props});
  final ProviderFormProps props;

  @override
  State<_GCPSMProviderForm> createState() => _GCPSMProviderFormState();
}

class _GCPSMProviderFormState extends State<_GCPSMProviderForm> {
  late _GCPSMAuthMethod _method;

  @override
  void initState() {
    super.initState();
    _method = _detectMethod(widget.props.spec);
  }

  void _setMethod(_GCPSMAuthMethod m) {
    if (_method == m) return;
    setState(() => _method = m);
    final spec = widget.props.spec;
    // Preserve top-level fields (projectID, location); replace auth slate.
    final next = Map<String, dynamic>.from(spec);
    if (m == _GCPSMAuthMethod.defaultCreds) {
      next.remove('auth');
    } else if (m == _GCPSMAuthMethod.workloadIdentity) {
      next['auth'] = {
        'workloadIdentity': {'serviceAccountRef': <String, dynamic>{}},
      };
    } else {
      next['auth'] = {
        'secretRef': {'secretAccessKeySecretRef': <String, dynamic>{}},
      };
    }
    widget.props.onUpdateSpec(next);
  }

  void _patchWI(Map<String, dynamic> patch) {
    final spec = widget.props.spec;
    final auth = Map<String, dynamic>.from(
        spec['auth'] is Map<String, dynamic>
            ? spec['auth'] as Map<String, dynamic>
            : <String, dynamic>{});
    final wi = Map<String, dynamic>.from(
        auth['workloadIdentity'] is Map<String, dynamic>
            ? auth['workloadIdentity'] as Map<String, dynamic>
            : <String, dynamic>{});
    wi.addAll(patch);
    auth['workloadIdentity'] = wi;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  void _patchSAKeyRef(Map<String, dynamic> patch) {
    final spec = widget.props.spec;
    final auth = Map<String, dynamic>.from(
        spec['auth'] is Map<String, dynamic>
            ? spec['auth'] as Map<String, dynamic>
            : <String, dynamic>{});
    final sr = Map<String, dynamic>.from(
        auth['secretRef'] is Map<String, dynamic>
            ? auth['secretRef'] as Map<String, dynamic>
            : <String, dynamic>{});
    final existing = Map<String, dynamic>.from(
        sr['secretAccessKeySecretRef'] is Map<String, dynamic>
            ? sr['secretAccessKeySecretRef'] as Map<String, dynamic>
            : <String, dynamic>{});
    existing.addAll(patch);
    sr['secretAccessKeySecretRef'] = existing;
    auth['secretRef'] = sr;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
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
            'Configure the GCP Secret Manager connection. '
            'The project ID is the only required field. '
            'Credentials are referenced by name from Kubernetes Secrets — '
            'this wizard never holds GCP credentials directly.',
            style: theme.textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: 16),

        // Top-level fields
        _ResyncTextField(
          value: props.getString('projectID'),
          label: 'Project ID *',
          hint: 'my-gcp-project',
          helper: 'GCP project ID where your secrets are stored.',
          error: errors['projectID'],
          onChanged: (v) => props.patchTop('projectID', v),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: props.getString('location'),
          label: 'Location (optional)',
          hint: 'us-central1',
          helper: 'Regional endpoint. Leave blank to use the global endpoint.',
          error: errors['location'],
          onChanged: (v) => props.patchTop('location', v),
        ),
        const SizedBox(height: 20),

        // Auth method picker
        const WizardSectionHeader(
          'Authentication method',
          subtitle:
              'Workload Identity is recommended for GKE clusters. '
              'SA Key (JSON) is simpler but requires long-lived credentials.',
        ),
        const SizedBox(height: 8),
        _AuthMethodPicker(
          selected: _method,
          onSelect: _setMethod,
          error: errors['auth'],
        ),
        const SizedBox(height: 16),

        // Auth-method-specific fields
        if (_method == _GCPSMAuthMethod.workloadIdentity) ...[
          _WorkloadIdentityFields(
            wiBlock: () {
              final auth = props.spec['auth'];
              if (auth is Map<String, dynamic>) {
                final wi = auth['workloadIdentity'];
                if (wi is Map<String, dynamic>) return wi;
              }
              return const <String, dynamic>{};
            }(),
            errors: errors,
            onPatch: _patchWI,
          ),
        ] else if (_method == _GCPSMAuthMethod.secretRef) ...[
          _SAKeyFields(
            sakRefBlock: () {
              final auth = props.spec['auth'];
              if (auth is Map<String, dynamic>) {
                final sr = auth['secretRef'];
                if (sr is Map<String, dynamic>) {
                  final ref = sr['secretAccessKeySecretRef'];
                  if (ref is Map<String, dynamic>) return ref;
                }
              }
              return const <String, dynamic>{};
            }(),
            errors: errors,
            onPatch: _patchSAKeyRef,
          ),
        ] else ...[
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              border: Border.all(color: theme.colorScheme.outlineVariant),
              borderRadius: BorderRadius.circular(6),
            ),
            child: Text(
              'No additional configuration required. ESO will use Application '
              'Default Credentials — typically the GKE node service account or '
              'a metadata-server-provided identity. Ensure the identity has '
              'secretmanager.versions.access permission on the target project.',
              style: theme.textTheme.bodySmall,
            ),
          ),
        ],
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Auth method picker chips
// ---------------------------------------------------------------------------

class _AuthMethodPicker extends StatelessWidget {
  const _AuthMethodPicker({
    required this.selected,
    required this.onSelect,
    this.error,
  });

  final _GCPSMAuthMethod selected;
  final ValueChanged<_GCPSMAuthMethod> onSelect;
  final String? error;

  static const _methods = [
    (
      method: _GCPSMAuthMethod.workloadIdentity,
      label: 'Workload Identity',
      description:
          'GKE Workload Identity — maps a Kubernetes ServiceAccount to a GCP service account.',
    ),
    (
      method: _GCPSMAuthMethod.secretRef,
      label: 'SA Key (JSON)',
      description:
          'Service Account JSON key stored in a Kubernetes Secret. Simpler but less secure.',
    ),
    (
      method: _GCPSMAuthMethod.defaultCreds,
      label: 'Default Credentials',
      description:
          'Use the node/pod identity (Application Default Credentials or GKE metadata server). No extra config required.',
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
          Text(error!, style: TextStyle(color: theme.colorScheme.error, fontSize: 12)),
        ],
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Workload Identity fields
// ---------------------------------------------------------------------------

class _WorkloadIdentityFields extends StatelessWidget {
  const _WorkloadIdentityFields({
    required this.wiBlock,
    required this.errors,
    required this.onPatch,
  });

  final Map<String, dynamic> wiBlock;
  final Map<String, String> errors;
  final ValueChanged<Map<String, dynamic>> onPatch;

  String _saName() {
    final ref = wiBlock['serviceAccountRef'];
    if (ref is Map<String, dynamic>) {
      final n = ref['name'];
      return n is String ? n : '';
    }
    return '';
  }

  String _str(String key) {
    final v = wiBlock[key];
    return v is String ? v : '';
  }

  void _patchSAName(String name) {
    final existing = wiBlock['serviceAccountRef'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(
            wiBlock['serviceAccountRef'] as Map<String, dynamic>)
        : <String, dynamic>{};
    if (name.isEmpty) {
      existing.remove('name');
    } else {
      existing['name'] = name;
    }
    onPatch({'serviceAccountRef': existing});
  }

  void _patchOptional(String key, String value) {
    final patch = <String, dynamic>{};
    if (value.isEmpty) {
      // Remove key — pass the whole current block minus the key.
      final next = Map<String, dynamic>.from(wiBlock);
      next.remove(key);
      onPatch(next);
      return;
    }
    patch[key] = value;
    onPatch(patch);
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader('Kubernetes ServiceAccount'),
        const SizedBox(height: 8),
        _ResyncTextField(
          value: _saName(),
          label: 'ServiceAccount name *',
          hint: 'eso-gcp-sa',
          helper:
              'The Kubernetes ServiceAccount annotated with the GCP service account email via Workload Identity.',
          error: errors['auth.workloadIdentity.serviceAccountRef.name'] ??
              errors['auth.workloadIdentity.serviceAccountRef'],
          onChanged: _patchSAName,
        ),
        const SizedBox(height: 16),
        const WizardSectionHeader(
          'Cluster details (optional)',
          subtitle: 'Read from the GKE metadata server when omitted.',
        ),
        const SizedBox(height: 8),
        _ResyncTextField(
          value: _str('clusterLocation'),
          label: 'Cluster location',
          hint: 'us-central1',
          error: errors['auth.workloadIdentity.clusterLocation'],
          onChanged: (v) => _patchOptional('clusterLocation', v),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: _str('clusterName'),
          label: 'Cluster name',
          hint: 'my-cluster',
          error: errors['auth.workloadIdentity.clusterName'],
          onChanged: (v) => _patchOptional('clusterName', v),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: _str('clusterProjectID'),
          label: 'Cluster project ID',
          hint: 'my-infra-project',
          error: errors['auth.workloadIdentity.clusterProjectID'],
          onChanged: (v) => _patchOptional('clusterProjectID', v),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// SA Key (JSON) fields
// ---------------------------------------------------------------------------

class _SAKeyFields extends StatelessWidget {
  const _SAKeyFields({
    required this.sakRefBlock,
    required this.errors,
    required this.onPatch,
  });

  final Map<String, dynamic> sakRefBlock;
  final Map<String, String> errors;
  final ValueChanged<Map<String, dynamic>> onPatch;

  String _str(String key) {
    final v = sakRefBlock[key];
    return v is String ? v : '';
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const WizardSectionHeader('Service Account key Secret reference'),
        const SizedBox(height: 4),
        Text(
          'The Kubernetes Secret containing the GCP Service Account JSON key file '
          '(downloaded from IAM & Admin → Service Accounts → Keys).',
          style: Theme.of(context).textTheme.bodySmall,
        ),
        const SizedBox(height: 8),
        _ResyncTextField(
          value: _str('name'),
          label: 'Secret name *',
          hint: 'gcp-sa-key',
          error: errors['auth.secretRef.secretAccessKeySecretRef.name'],
          onChanged: (v) => onPatch(
            v.isEmpty
                ? (Map<String, dynamic>.from(sakRefBlock)..remove('name'))
                : {...sakRefBlock, 'name': v},
          ),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: _str('key'),
          label: 'Key *',
          hint: 'key.json',
          helper: 'The key within the Secret holding the SA JSON content.',
          error: errors['auth.secretRef.secretAccessKeySecretRef.key'],
          onChanged: (v) => onPatch(
            v.isEmpty
                ? (Map<String, dynamic>.from(sakRefBlock)..remove('key'))
                : {...sakRefBlock, 'key': v},
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Reusable stateful text field — mirrors _AcmeServerField cursor-safe resync
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
    // Only resync on an external value change — not on every keystroke echo.
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
