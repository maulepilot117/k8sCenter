// First-phase widget of the ESO Bulk Refresh modal sheet. Mobile-only —
// the web `ESOBulkRefreshDialog.tsx` is launched from per-store /
// per-namespace contexts, so it doesn't need a scope-pick UI. Mobile
// consolidates the entry point onto a single dashboard button, which
// forces the operator to choose:
//
//   * Store          — refresh every ES backed by ns/storeName
//   * ClusterStore   — refresh every ES backed by clusterStoreName
//   * Namespace      — refresh every ES in this namespace
//
// Once the operator picks a variant + identifier and taps "Continue",
// the parent sheet calls `BulkRefreshController.beginScopeLoad(scope)`
// and advances to the scopeLoad phase.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../api/eso_repository.dart';
import '../../theme/kube_theme_builder.dart';
import '../../widgets/named_resource_picker.dart';
import 'bulk_refresh_controller.dart';

enum _ScopeVariant { store, clusterStore, namespace }

class BulkRefreshScopePicker extends ConsumerStatefulWidget {
  const BulkRefreshScopePicker({
    super.key,
    required this.clusterId,
    required this.onSubmit,
    required this.onCancel,
  });

  /// Pinned cluster id from the parent sheet. Threaded into every
  /// list provider so the picker can't surface a different cluster's
  /// stores under this cluster's slot.
  final String clusterId;

  /// Called when the operator taps "Continue" and a valid scope is
  /// selected.
  final ValueChanged<BulkRefreshScope> onSubmit;

  /// Called when the operator taps "Cancel" / dismisses.
  final VoidCallback onCancel;

  @override
  ConsumerState<BulkRefreshScopePicker> createState() =>
      _BulkRefreshScopePickerState();
}

class _BulkRefreshScopePickerState
    extends ConsumerState<BulkRefreshScopePicker> {
  _ScopeVariant _variant = _ScopeVariant.namespace;

  String _namespace = '';
  String _storeName = '';
  String _clusterStoreName = '';

  bool get _canContinue {
    return switch (_variant) {
      _ScopeVariant.store => _namespace.isNotEmpty && _storeName.isNotEmpty,
      _ScopeVariant.clusterStore => _clusterStoreName.isNotEmpty,
      _ScopeVariant.namespace => _namespace.isNotEmpty,
    };
  }

  BulkRefreshScope? _buildScope() {
    return switch (_variant) {
      _ScopeVariant.store =>
        BulkRefreshScopeStore(namespace: _namespace, name: _storeName),
      _ScopeVariant.clusterStore =>
        BulkRefreshScopeClusterStore(name: _clusterStoreName),
      _ScopeVariant.namespace =>
        BulkRefreshScopeNamespace(namespace: _namespace),
    };
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'What should refresh?',
          style: TextStyle(
            color: colors.textPrimary,
            fontSize: 17,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 12),
        SegmentedButton<_ScopeVariant>(
          segments: const [
            ButtonSegment(
              value: _ScopeVariant.namespace,
              label: Text('Namespace'),
              icon: Icon(Icons.folder_outlined, size: 16),
            ),
            ButtonSegment(
              value: _ScopeVariant.store,
              label: Text('Store'),
              icon: Icon(Icons.inventory_2_outlined, size: 16),
            ),
            ButtonSegment(
              value: _ScopeVariant.clusterStore,
              label: Text('Cluster store'),
              icon: Icon(Icons.public, size: 16),
            ),
          ],
          selected: {_variant},
          onSelectionChanged: (s) {
            if (s.isEmpty) return;
            setState(() {
              _variant = s.first;
            });
          },
        ),
        const SizedBox(height: 16),
        _variantHelper(colors),
        const SizedBox(height: 12),
        ..._variantBody(),
        const SizedBox(height: 20),
        Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            TextButton(
              onPressed: widget.onCancel,
              style: TextButton.styleFrom(
                foregroundColor: colors.textSecondary,
              ),
              child: const Text('Cancel'),
            ),
            const SizedBox(width: 8),
            FilledButton(
              onPressed: _canContinue
                  ? () {
                      final scope = _buildScope();
                      if (scope != null) widget.onSubmit(scope);
                    }
                  : null,
              style: FilledButton.styleFrom(
                backgroundColor: colors.accent,
                foregroundColor: Colors.white,
              ),
              child: const Text('Continue'),
            ),
          ],
        ),
      ],
    );
  }

  Widget _variantHelper(KubeColors colors) {
    final text = switch (_variant) {
      _ScopeVariant.store =>
        'Refreshes every ExternalSecret in the chosen namespace that '
            'references the chosen SecretStore.',
      _ScopeVariant.clusterStore =>
        'Refreshes every ExternalSecret across the cluster that '
            'references the chosen ClusterSecretStore.',
      _ScopeVariant.namespace =>
        'Refreshes every ExternalSecret in the chosen namespace.',
    };
    return Text(
      text,
      style: TextStyle(color: colors.textSecondary, fontSize: 12, height: 1.4),
    );
  }

  List<Widget> _variantBody() {
    switch (_variant) {
      case _ScopeVariant.namespace:
        return [
          NamedResourcePicker(
            clusterId: widget.clusterId,
            kind: 'namespaces',
            namespace: null,
            selected: _namespace,
            onChanged: (v) => setState(() => _namespace = v),
            label: 'Namespace',
            hint: 'Pick a namespace',
          ),
        ];
      case _ScopeVariant.store:
        return [
          NamedResourcePicker(
            clusterId: widget.clusterId,
            kind: 'namespaces',
            namespace: null,
            selected: _namespace,
            onChanged: (v) => setState(() {
              _namespace = v;
              // Resetting the store-name selection when the namespace
              // changes prevents a stale name from a different namespace
              // bleeding into the submitted scope.
              _storeName = '';
            }),
            label: 'Namespace',
            hint: 'Pick a namespace',
          ),
          const SizedBox(height: 12),
          if (_namespace.isEmpty)
            _SecondaryHint(text: 'Pick a namespace first to list its stores.')
          else
            _StorePicker(
              clusterId: widget.clusterId,
              namespace: _namespace,
              selected: _storeName,
              onChanged: (v) => setState(() => _storeName = v),
            ),
        ];
      case _ScopeVariant.clusterStore:
        return [
          _ClusterStorePicker(
            clusterId: widget.clusterId,
            selected: _clusterStoreName,
            onChanged: (v) => setState(() => _clusterStoreName = v),
          ),
        ];
    }
  }
}

class _SecondaryHint extends StatelessWidget {
  const _SecondaryHint({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Text(
        text,
        style: TextStyle(color: colors.textMuted, fontSize: 12),
      ),
    );
  }
}

/// Bordered "labeled box" used by both store pickers. Extracted from
/// duplicate `_frame` helpers that used to live on `_StorePicker` and
/// `_ClusterStorePicker` — the cluster-store variant previously
/// hardcoded its label, which made the two helpers structurally
/// identical but textually inconsistent. Threading the label through
/// here removes the duplication and the inconsistency in one move.
class _LabeledPickerFrame extends StatelessWidget {
  const _LabeledPickerFrame({required this.label, required this.child});

  final String label;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: TextStyle(
              color: colors.textMuted,
              fontSize: 11,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          child,
        ],
      ),
    );
  }
}

/// Namespaced SecretStore picker. Reads `storesListProvider` with the
/// chosen namespace in the family key so the backend filters server-side
/// via `?namespace=` (no client-side `.where` walk over an unbounded
/// cluster-wide list — large fleets used to pay the entire payload cost
/// just to render the dropdown for one namespace).
class _StorePicker extends ConsumerWidget {
  const _StorePicker({
    required this.clusterId,
    required this.namespace,
    required this.selected,
    required this.onChanged,
  });

  final String clusterId;
  final String namespace;
  final String selected;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(storesListProvider(
      StoresListKey(clusterId: clusterId, namespace: namespace),
    ));
    return async.when(
      loading: () => const _LabeledPickerFrame(
        label: 'SecretStore',
        child: LinearProgressIndicator(minHeight: 2),
      ),
      error: (e, _) => _LabeledPickerFrame(
        label: 'SecretStore',
        child: Text(
          'Failed to load stores: $e',
          style: TextStyle(color: colors.error, fontSize: 12),
        ),
      ),
      data: (stores) {
        // Backend already filters by namespace; we just project to names.
        final filtered = stores.map((s) => s.name).toList()..sort();
        if (filtered.isEmpty) {
          return _LabeledPickerFrame(
            label: 'SecretStore',
            child: Text(
              'No SecretStores in $namespace',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          );
        }
        if (selected.isNotEmpty && !filtered.contains(selected)) {
          filtered.add(selected);
          filtered.sort();
        }
        return DropdownButtonFormField<String>(
          initialValue: selected.isEmpty ? null : selected,
          isExpanded: true,
          decoration: const InputDecoration(
            labelText: 'SecretStore',
            border: OutlineInputBorder(),
          ),
          items: [
            for (final v in filtered)
              DropdownMenuItem(value: v, child: Text(v)),
          ],
          onChanged: (v) {
            if (v == null) return;
            onChanged(v);
          },
        );
      },
    );
  }
}

class _ClusterStorePicker extends ConsumerWidget {
  const _ClusterStorePicker({
    required this.clusterId,
    required this.selected,
    required this.onChanged,
  });

  final String clusterId;
  final String selected;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final async = ref.watch(clusterStoresListProvider(clusterId));
    return async.when(
      loading: () => const _LabeledPickerFrame(
        label: 'ClusterSecretStore',
        child: LinearProgressIndicator(minHeight: 2),
      ),
      error: (e, _) => _LabeledPickerFrame(
        label: 'ClusterSecretStore',
        child: Text(
          'Failed to load cluster stores: $e',
          style: TextStyle(color: colors.error, fontSize: 12),
        ),
      ),
      data: (stores) {
        final names = stores.map((s) => s.name).toList()..sort();
        if (names.isEmpty) {
          return _LabeledPickerFrame(
            label: 'ClusterSecretStore',
            child: Text(
              'No ClusterSecretStores on this cluster',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          );
        }
        if (selected.isNotEmpty && !names.contains(selected)) {
          names.add(selected);
          names.sort();
        }
        return DropdownButtonFormField<String>(
          initialValue: selected.isEmpty ? null : selected,
          isExpanded: true,
          decoration: const InputDecoration(
            labelText: 'ClusterSecretStore',
            border: OutlineInputBorder(),
          ),
          items: [
            for (final v in names) DropdownMenuItem(value: v, child: Text(v)),
          ],
          onChanged: (v) {
            if (v == null) return;
            onChanged(v);
          },
        );
      },
    );
  }
}

