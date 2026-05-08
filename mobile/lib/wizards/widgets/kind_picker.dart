// Generic single-select picker over a fixed set of Kubernetes kinds.
// Used by HPA (scaleTargetRef.kind ∈ Deployment/StatefulSet/ReplicaSet)
// and RoleBinding (roleRef.kind ∈ Role/ClusterRole). Renders as a
// segmented row of [ChoiceChip]s — small enough on phone, no dropdown
// indirection.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

class KindPickerOption {
  const KindPickerOption({required this.value, required this.label});

  /// Wire value (e.g., "Deployment", "ClusterRole"). What the wizard's
  /// form record stores and what the backend expects.
  final String value;

  /// Display label. Defaults to [value] in callers that don't need a
  /// nicer presentation.
  final String label;
}

class KindPicker extends StatelessWidget {
  const KindPicker({
    super.key,
    required this.options,
    required this.selected,
    required this.onChanged,
    this.label,
    this.errorMessage,
  });

  final List<KindPickerOption> options;
  final String selected;
  final ValueChanged<String> onChanged;

  /// Optional small caption rendered above the chip row.
  final String? label;

  /// Inline error rendered under the row.
  final String? errorMessage;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (label != null) ...[
          Text(
            label!,
            style: TextStyle(
              color: colors.textPrimary,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 6),
        ],
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final opt in options)
              ChoiceChip(
                label: Text(opt.label),
                selected: selected == opt.value,
                onSelected: (sel) {
                  if (sel) onChanged(opt.value);
                },
              ),
          ],
        ),
        if (errorMessage != null) ...[
          const SizedBox(height: 6),
          Text(
            errorMessage!,
            style: TextStyle(color: colors.error, fontSize: 12),
          ),
        ],
      ],
    );
  }
}
