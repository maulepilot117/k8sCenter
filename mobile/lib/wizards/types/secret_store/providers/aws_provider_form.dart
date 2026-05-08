// AWS Secrets Manager provider form for the SecretStore wizard. Stateless
// dumb widget — no riverpod, no dio.
//
// Wire format (spec.provider.aws shape mirrored from ESO):
//   {
//     region: String,               // required
//     role?:  String,               // optional assume-role ARN
//     auth: {                       // exactly one of:
//       jwt:       { serviceAccountRef: { name } }
//       secretRef: { accessKeyIDSecretRef:    { name, key },
//                    secretAccessKeySecretRef: { name, key } }
//     }
//   }
//
// NOTE: `service` is intentionally absent. ESO defaults to SecretsManager
// when service is omitted. The synthetic "awsps" discriminator injects
// `service: ParameterStore` upstream in backend ToSecretStore — that form
// is awsps_provider_form.dart.
//
// Backend validator: backend/internal/wizard/secretstore_aws.go
// Web ground-truth:  frontend/components/wizard/secretstore/AWSForm.tsx
//
// Default auth method: jwt (IRSA — the modern EKS cluster pattern).
// Switching auth method calls onUpdateSpec({...spec, auth: {[m]: emptyBlock}})
// mirroring AWSForm.tsx setMethod exactly.

import 'package:flutter/material.dart';

import 'provider_form.dart';
import '../../../widgets/section_header.dart';

// ---------------------------------------------------------------------------
// Auth method descriptor
// ---------------------------------------------------------------------------

const _kAuthMethods = [
  _AuthMethod(
    'jwt',
    'IAM / IRSA',
    'Workload identity: the pod\'s service account assumes an IAM role via IRSA or EKS Pod Identity.',
  ),
  _AuthMethod(
    'secretRef',
    'Static credentials',
    'Access Key ID + Secret Access Key pair stored in Kubernetes Secrets.',
  ),
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

/// Top-level builder for the AWS Secrets Manager provider form.
Widget awsProviderForm(ProviderFormProps props) =>
    _AWSProviderForm(props: props);

// ---------------------------------------------------------------------------
// Root stateful widget
// ---------------------------------------------------------------------------

class _AWSProviderForm extends StatefulWidget {
  const _AWSProviderForm({required this.props});
  final ProviderFormProps props;

  @override
  State<_AWSProviderForm> createState() => _AWSProviderFormState();
}

class _AWSProviderFormState extends State<_AWSProviderForm> {
  String _detectMethod(Map<String, dynamic> spec) {
    final auth = spec['auth'];
    if (auth is! Map<String, dynamic>) return 'jwt';
    for (final m in _kAuthMethods) {
      if (auth.containsKey(m.id)) return m.id;
    }
    return 'jwt';
  }

  Map<String, dynamic> _emptyBlock(String method) {
    switch (method) {
      case 'jwt':
        return <String, dynamic>{
          'serviceAccountRef': <String, dynamic>{}
        };
      case 'secretRef':
        return <String, dynamic>{
          'accessKeyIDSecretRef': <String, dynamic>{},
          'secretAccessKeySecretRef': <String, dynamic>{},
        };
      default:
        return <String, dynamic>{};
    }
  }

  void _setMethod(String method) {
    final spec = widget.props.spec;
    if (_detectMethod(spec) == method) return;
    final next = Map<String, dynamic>.from(spec);
    next['auth'] = <String, dynamic>{method: _emptyBlock(method)};
    widget.props.onUpdateSpec(next);
  }

  Map<String, dynamic> _authBlock(String method) {
    final auth = widget.props.spec['auth'];
    if (auth is! Map<String, dynamic>) return <String, dynamic>{};
    final block = auth[method];
    return block is Map<String, dynamic> ? block : <String, dynamic>{};
  }

  /// Patch auth[method][refField][subKey]. Used for the nested
  /// accessKeyIDSecretRef / secretAccessKeySecretRef sub-maps.
  void _patchSecretKeyRef(String refField, String subKey, String value) {
    final spec = widget.props.spec;
    final auth = Map<String, dynamic>.from(
        spec['auth'] is Map<String, dynamic>
            ? spec['auth'] as Map<String, dynamic>
            : <String, dynamic>{});
    final block = Map<String, dynamic>.from(
        auth['secretRef'] is Map<String, dynamic>
            ? auth['secretRef'] as Map<String, dynamic>
            : <String, dynamic>{});
    final ref = Map<String, dynamic>.from(
        block[refField] is Map<String, dynamic>
            ? block[refField] as Map<String, dynamic>
            : <String, dynamic>{});
    if (value.isEmpty) {
      ref.remove(subKey);
    } else {
      ref[subKey] = value;
    }
    block[refField] = ref;
    auth['secretRef'] = block;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  /// Patch auth.jwt.serviceAccountRef.name.
  void _patchSARefName(String value) {
    final spec = widget.props.spec;
    final auth = Map<String, dynamic>.from(
        spec['auth'] is Map<String, dynamic>
            ? spec['auth'] as Map<String, dynamic>
            : <String, dynamic>{});
    final jwtBlock = Map<String, dynamic>.from(
        auth['jwt'] is Map<String, dynamic>
            ? auth['jwt'] as Map<String, dynamic>
            : <String, dynamic>{});
    final existing = Map<String, dynamic>.from(
        jwtBlock['serviceAccountRef'] is Map<String, dynamic>
            ? jwtBlock['serviceAccountRef'] as Map<String, dynamic>
            : <String, dynamic>{});
    if (value.isEmpty) {
      existing.remove('name');
    } else {
      existing['name'] = value;
    }
    jwtBlock['serviceAccountRef'] = existing;
    auth['jwt'] = jwtBlock;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  @override
  Widget build(BuildContext context) {
    final props = widget.props;
    final spec = props.spec;
    final errors = props.errors;
    final activeMethod = _detectMethod(spec);

    // Read nested refs for static-creds method.
    final srBlock = _authBlock('secretRef');
    final akRef = srBlock['accessKeyIDSecretRef'];
    final sakRef = srBlock['secretAccessKeySecretRef'];
    final akRefMap =
        akRef is Map<String, dynamic> ? akRef : <String, dynamic>{};
    final sakRefMap =
        sakRef is Map<String, dynamic> ? sakRef : <String, dynamic>{};

    // Read jwt.serviceAccountRef.name.
    final jwtBlock = _authBlock('jwt');
    final saRefRaw = jwtBlock['serviceAccountRef'];
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
            'Configure the AWS Secrets Manager connection and authentication method. '
            'Credentials must already exist as Kubernetes Secrets in this namespace; '
            'this wizard only references them.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: 16),

        // --- Top-level fields ---
        const WizardSectionHeader('Connection'),
        const SizedBox(height: 8),
        Row(
          children: [
            Expanded(
              child: _TextField(
                value: props.getString('region'),
                label: 'AWS Region',
                hint: 'us-east-1',
                helper: 'The AWS region where your secrets are stored.',
                errorText: errors['region'],
                onChanged: (v) => props.patchTop('region', v),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _TextField(
                value: props.getString('role'),
                label: 'Assume-role ARN (optional)',
                hint: 'arn:aws:iam::123456789012:role/my-role',
                helper:
                    'IAM role to assume before fetching secrets. Leave blank to use the pod\'s own identity.',
                errorText: errors['role'],
                onChanged: (v) => props.patchTop('role', v),
              ),
            ),
          ],
        ),
        const SizedBox(height: 20),

        // --- Auth method picker ---
        const WizardSectionHeader(
          'Authentication method',
          subtitle: 'Select one — switching resets the auth fields.',
        ),
        const SizedBox(height: 8),
        Row(
          children: [
            for (final m in _kAuthMethods) ...[
              Expanded(
                child: _AuthChip(
                  method: m,
                  selected: activeMethod == m.id,
                  onTap: () => _setMethod(m.id),
                ),
              ),
              if (m != _kAuthMethods.last) const SizedBox(width: 8),
            ],
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
        if (activeMethod == 'jwt')
          _AuthBox(
            title: 'IAM / IRSA — service account reference',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'The service account must have an IAM role ARN annotation '
                  '(eks.amazonaws.com/role-arn) or be bound via EKS Pod Identity. '
                  'The assume-role ARN above takes precedence if set.',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 12),
                _TextField(
                  value: saName,
                  label: 'Service account name',
                  hint: 'my-service-account',
                  helper:
                      'The Kubernetes ServiceAccount whose projected token is exchanged for AWS credentials.',
                  errorText: errors['auth.jwt.serviceAccountRef.name'],
                  onChanged: _patchSARefName,
                ),
              ],
            ),
          ),

        if (activeMethod == 'secretRef')
          _AuthBox(
            title: 'Static credentials',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Access Key ID Secret reference',
                    style: Theme.of(context)
                        .textTheme
                        .labelMedium
                        ?.copyWith(fontWeight: FontWeight.w600)),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: _TextField(
                        value: akRefMap['name'] is String
                            ? akRefMap['name'] as String
                            : '',
                        label: 'Secret name',
                        hint: 'aws-credentials',
                        errorText: errors[
                            'auth.secretRef.accessKeyIDSecretRef.name'],
                        onChanged: (v) => _patchSecretKeyRef(
                            'accessKeyIDSecretRef', 'name', v),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _TextField(
                        value: akRefMap['key'] is String
                            ? akRefMap['key'] as String
                            : '',
                        label: 'Key',
                        hint: 'access-key-id',
                        errorText:
                            errors['auth.secretRef.accessKeyIDSecretRef.key'],
                        onChanged: (v) => _patchSecretKeyRef(
                            'accessKeyIDSecretRef', 'key', v),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                Text('Secret Access Key Secret reference',
                    style: Theme.of(context)
                        .textTheme
                        .labelMedium
                        ?.copyWith(fontWeight: FontWeight.w600)),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: _TextField(
                        value: sakRefMap['name'] is String
                            ? sakRefMap['name'] as String
                            : '',
                        label: 'Secret name',
                        hint: 'aws-credentials',
                        errorText: errors[
                            'auth.secretRef.secretAccessKeySecretRef.name'],
                        onChanged: (v) => _patchSecretKeyRef(
                            'secretAccessKeySecretRef', 'name', v),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _TextField(
                        value: sakRefMap['key'] is String
                            ? sakRefMap['key'] as String
                            : '',
                        label: 'Key',
                        hint: 'secret-access-key',
                        errorText: errors[
                            'auth.secretRef.secretAccessKeySecretRef.key'],
                        onChanged: (v) => _patchSecretKeyRef(
                            'secretAccessKeySecretRef', 'key', v),
                      ),
                    ),
                  ],
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
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(method.label,
                style: TextStyle(
                    fontWeight: FontWeight.w600,
                    color: selected
                        ? colorScheme.primary
                        : colorScheme.onSurface)),
            const SizedBox(height: 4),
            Text(method.description,
                style: TextStyle(
                    fontSize: 11, color: colorScheme.onSurfaceVariant)),
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
