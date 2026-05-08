// Numeric text field where blank means "leave field unset" (the
// backend treats nil pointers as defaults). Avoids the trap where 0
// is a valid value but blank should mean "omit". Used by Job's
// parallelism/completions/backoffLimit; available to any wizard that
// surfaces optional int fields (HPA min/max, PDB minAvailable, etc.).

import 'package:flutter/material.dart';

class OptionalIntField extends StatefulWidget {
  const OptionalIntField({
    super.key,
    required this.label,
    required this.hint,
    required this.value,
    required this.onChanged,
    this.error,
  });

  final String label;
  final String hint;
  final int? value;
  final String? error;
  final ValueChanged<int?> onChanged;

  @override
  State<OptionalIntField> createState() => _OptionalIntFieldState();
}

class _OptionalIntFieldState extends State<OptionalIntField> {
  late final TextEditingController _ctl =
      TextEditingController(text: widget.value?.toString() ?? '');

  @override
  void didUpdateWidget(covariant OptionalIntField oldWidget) {
    super.didUpdateWidget(oldWidget);
    final next = widget.value?.toString() ?? '';
    if (_ctl.text != next) _ctl.text = next;
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
      keyboardType: TextInputType.number,
      onChanged: (v) {
        final s = v.trim();
        if (s.isEmpty) {
          widget.onChanged(null);
          return;
        }
        final n = int.tryParse(s);
        // Silently no-op on unparseable input — operator's last good
        // value stays in form state until they correct the field.
        if (n == null) return;
        widget.onChanged(n);
      },
      decoration: InputDecoration(
        labelText: widget.label,
        hintText: widget.hint,
        border: const OutlineInputBorder(),
        errorText: widget.error,
      ),
    );
  }
}
