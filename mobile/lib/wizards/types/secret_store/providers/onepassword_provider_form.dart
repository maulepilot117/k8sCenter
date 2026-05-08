// 1Password Connect provider form for SecretStoreWizard.
//
// Wire format written into providerSpec:
//   {
//     connectHost: string,          // required; must use https
//     auth: {
//       secretRef: {
//         connectTokenSecretRef: {  // required
//           name: string,
//           key:  string,
//         }
//       }
//     },
//     vaults: Map<String, int>,     // required; at least one entry
//                                   // vault name → search priority (int, lower = first)
//   }
//
// Backend validator: backend/internal/wizard/secretstore_onepassword.go
// Web ground-truth: frontend/components/wizard/secretstore/OnePasswordForm.tsx
//
// No auth method picker — only one auth path (Connect token via secretRef).
//
// Vaults are managed as an in-memory list of (_VaultEntry) rows, each holding
// a name and a string priority (to preserve the in-progress text input before
// it's parsed to int). The list is converted to Map<String, int> on every
// change via _rowsToVaultsMap. One sentinel row is shown by default.
//
// All text inputs are stateful (_ResyncTextField / _VaultRow) to avoid cursor-jumping.

import 'package:flutter/material.dart';

import 'provider_form.dart';
import '../../../widgets/section_header.dart';
import '../../../widgets/repeating_row_group.dart';

// ---------------------------------------------------------------------------
// Vault entry model (local form state only)
// ---------------------------------------------------------------------------

class _VaultEntry {
  _VaultEntry({this.name = '', this.priority = '1'});
  String name;
  String priority;
}

List<_VaultEntry> _vaultsFromSpec(Map<String, dynamic> spec) {
  final raw = spec['vaults'];
  if (raw is Map<String, dynamic> && raw.isNotEmpty) {
    return raw.entries
        .map((e) => _VaultEntry(name: e.key, priority: '${e.value ?? 1}'))
        .toList();
  }
  return [_VaultEntry()];
}

Map<String, dynamic> _rowsToVaultsMap(List<_VaultEntry> rows) {
  final out = <String, dynamic>{};
  for (final row in rows) {
    final name = row.name.trim();
    if (name.isEmpty) continue;
    final p = int.tryParse(row.priority);
    out[name] = p ?? 1;
  }
  return out;
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

Widget onepasswordProviderForm(ProviderFormProps props) =>
    _OnePasswordProviderForm(props: props);

// ---------------------------------------------------------------------------
// Root stateful widget
// ---------------------------------------------------------------------------

class _OnePasswordProviderForm extends StatefulWidget {
  const _OnePasswordProviderForm({required this.props});
  final ProviderFormProps props;

  @override
  State<_OnePasswordProviderForm> createState() =>
      _OnePasswordProviderFormState();
}

class _OnePasswordProviderFormState extends State<_OnePasswordProviderForm> {
  late List<_VaultEntry> _vaults;

  @override
  void initState() {
    super.initState();
    _vaults = _vaultsFromSpec(widget.props.spec);
  }

  // Read the Connect token ref from spec (deep path).
  Map<String, dynamic> _tokenRef() {
    final auth = widget.props.spec['auth'];
    if (auth is Map<String, dynamic>) {
      final sr = auth['secretRef'];
      if (sr is Map<String, dynamic>) {
        final ctr = sr['connectTokenSecretRef'];
        if (ctr is Map<String, dynamic>) return ctr;
      }
    }
    return const <String, dynamic>{};
  }

  void _patchTokenRef(Map<String, dynamic> patch) {
    final spec = widget.props.spec;
    final auth = spec['auth'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(spec['auth'] as Map<String, dynamic>)
        : <String, dynamic>{};
    final sr = auth['secretRef'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(auth['secretRef'] as Map<String, dynamic>)
        : <String, dynamic>{};
    final existing = sr['connectTokenSecretRef'] is Map<String, dynamic>
        ? Map<String, dynamic>.from(
            sr['connectTokenSecretRef'] as Map<String, dynamic>)
        : <String, dynamic>{};
    existing.addAll(patch);
    sr['connectTokenSecretRef'] = existing;
    auth['secretRef'] = sr;
    widget.props.onUpdateSpec(Map<String, dynamic>.from(spec)..['auth'] = auth);
  }

  void _commitVaults(List<_VaultEntry> updated) {
    setState(() => _vaults = updated);
    final map = _rowsToVaultsMap(updated);
    final next = Map<String, dynamic>.from(widget.props.spec);
    next['vaults'] = map;
    widget.props.onUpdateSpec(next);
  }

  void _addVault() => _commitVaults([..._vaults, _VaultEntry()]);

  void _removeVault(int i) {
    final updated = [..._vaults]..removeAt(i);
    _commitVaults(
        updated.isEmpty ? [_VaultEntry()] : updated);
  }

  void _updateVaultName(int i, String name) {
    final updated = [..._vaults];
    updated[i] = _VaultEntry(name: name, priority: updated[i].priority);
    _commitVaults(updated);
  }

  void _updateVaultPriority(int i, String priority) {
    final updated = [..._vaults];
    updated[i] = _VaultEntry(name: updated[i].name, priority: priority);
    _commitVaults(updated);
  }

  @override
  Widget build(BuildContext context) {
    final props = widget.props;
    final errors = props.errors;
    final theme = Theme.of(context);
    final tokenRef = _tokenRef();

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
            'Configure the 1Password Connect server connection and credentials. '
            'The Connect token must already exist as a Kubernetes Secret — this '
            'wizard only references it and never holds credentials directly.',
            style: theme.textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: 16),

        // Connect server URL
        _ResyncTextField(
          value: props.getString('connectHost'),
          label: 'Connect server URL *',
          hint: 'https://connect.example.com:8080',
          helper:
              'Must use https. Private and in-cluster addresses are accepted.',
          error: errors['connectHost'],
          onChanged: (v) => props.patchTop('connectHost', v),
        ),
        const SizedBox(height: 20),

        // Connect token secret reference
        const WizardSectionHeader('Connect token Secret reference'),
        const SizedBox(height: 4),
        Text(
          'Reference to the Kubernetes Secret that holds the 1Password Connect '
          'API token (auth.secretRef.connectTokenSecretRef).',
          style: theme.textTheme.bodySmall,
        ),
        const SizedBox(height: 8),
        if (errors['auth'] != null) ...[
          Text(
            errors['auth']!,
            style: TextStyle(color: theme.colorScheme.error, fontSize: 12),
          ),
          const SizedBox(height: 4),
        ],
        if (errors['auth.secretRef'] != null) ...[
          Text(
            errors['auth.secretRef']!,
            style: TextStyle(color: theme.colorScheme.error, fontSize: 12),
          ),
          const SizedBox(height: 4),
        ],
        if (errors['auth.secretRef.connectTokenSecretRef'] != null) ...[
          Text(
            errors['auth.secretRef.connectTokenSecretRef']!,
            style: TextStyle(color: theme.colorScheme.error, fontSize: 12),
          ),
          const SizedBox(height: 4),
        ],
        _ResyncTextField(
          value: tokenRef['name'] is String ? tokenRef['name'] as String : '',
          label: 'Secret name *',
          hint: 'op-connect-token',
          error: errors['auth.secretRef.connectTokenSecretRef.name'],
          onChanged: (v) => _patchTokenRef(
            v.isEmpty
                ? (Map<String, dynamic>.from(tokenRef)..remove('name'))
                : {...tokenRef, 'name': v},
          ),
        ),
        const SizedBox(height: 12),
        _ResyncTextField(
          value: tokenRef['key'] is String ? tokenRef['key'] as String : '',
          label: 'Key *',
          hint: 'token',
          helper: 'The key within the Secret that contains the token value.',
          error: errors['auth.secretRef.connectTokenSecretRef.key'],
          onChanged: (v) => _patchTokenRef(
            v.isEmpty
                ? (Map<String, dynamic>.from(tokenRef)..remove('key'))
                : {...tokenRef, 'key': v},
          ),
        ),
        const SizedBox(height: 20),

        // Vaults map
        const WizardSectionHeader(
          'Vaults *',
          subtitle:
              'Map each 1Password vault name to a search priority. '
              'ESO searches vaults in ascending priority order (lower number = searched first). '
              'At least one entry is required.',
        ),
        const SizedBox(height: 8),
        if (errors['vaults'] != null) ...[
          Text(
            errors['vaults']!,
            style: TextStyle(color: theme.colorScheme.error, fontSize: 12),
          ),
          const SizedBox(height: 4),
        ],

        // Column headers
        Row(
          children: [
            Expanded(
              child: Text(
                'VAULT NAME',
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w500,
                  color: theme.colorScheme.onSurfaceVariant,
                  letterSpacing: 0.5,
                ),
              ),
            ),
            SizedBox(
              width: 80,
              child: Text(
                'PRIORITY',
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w500,
                  color: theme.colorScheme.onSurfaceVariant,
                  letterSpacing: 0.5,
                ),
              ),
            ),
            // Space for the remove button rendered by RepeatingRowGroup
            const SizedBox(width: 40),
          ],
        ),
        const SizedBox(height: 4),

        RepeatingRowGroup<_VaultEntry>(
          items: _vaults,
          addLabel: 'Add vault',
          emptyMessage: 'No vaults added — at least one is required.',
          onAdd: _addVault,
          onRemove: _removeVault,
          itemBuilder: (ctx, i, entry) => _VaultRow(
            initialName: entry.name,
            initialPriority: entry.priority,
            onNameChanged: (v) => _updateVaultName(i, v),
            onPriorityChanged: (v) => _updateVaultPriority(i, v),
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Single vault row — stateful so text fields don't lose focus
// ---------------------------------------------------------------------------

class _VaultRow extends StatefulWidget {
  const _VaultRow({
    required this.initialName,
    required this.initialPriority,
    required this.onNameChanged,
    required this.onPriorityChanged,
  });

  final String initialName;
  final String initialPriority;
  final ValueChanged<String> onNameChanged;
  final ValueChanged<String> onPriorityChanged;

  @override
  State<_VaultRow> createState() => _VaultRowState();
}

class _VaultRowState extends State<_VaultRow> {
  late final TextEditingController _nameClt =
      TextEditingController(text: widget.initialName);
  late final TextEditingController _priClt =
      TextEditingController(text: widget.initialPriority);

  @override
  void didUpdateWidget(covariant _VaultRow old) {
    super.didUpdateWidget(old);
    if (widget.initialName != _nameClt.text &&
        widget.initialName != old.initialName) {
      _nameClt.text = widget.initialName;
      _nameClt.selection =
          TextSelection.collapsed(offset: widget.initialName.length);
    }
    if (widget.initialPriority != _priClt.text &&
        widget.initialPriority != old.initialPriority) {
      _priClt.text = widget.initialPriority;
      _priClt.selection =
          TextSelection.collapsed(offset: widget.initialPriority.length);
    }
  }

  @override
  void dispose() {
    _nameClt.dispose();
    _priClt.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        Expanded(
          child: TextField(
            controller: _nameClt,
            decoration: const InputDecoration(
              hintText: 'production',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            onChanged: widget.onNameChanged,
          ),
        ),
        const SizedBox(width: 8),
        SizedBox(
          width: 72,
          child: TextField(
            controller: _priClt,
            keyboardType: TextInputType.number,
            decoration: const InputDecoration(
              hintText: '1',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            onChanged: widget.onPriorityChanged,
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
