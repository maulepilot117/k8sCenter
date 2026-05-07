// Replica-count input for the Scale action. Returns the chosen non-negative
// integer (or null if the operator dismissed).

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../theme/kube_theme_builder.dart';

/// Show the scale sheet pre-filled with the resource's current desired
/// replicas. Returns the new replica count on submit, or null on dismiss.
Future<int?> showScaleSheet({
  required BuildContext context,
  required String name,
  required int currentReplicas,
}) {
  return showModalBottomSheet<int>(
    context: context,
    isScrollControlled: true,
    builder: (ctx) => ScaleSheet(name: name, currentReplicas: currentReplicas),
  );
}

class ScaleSheet extends StatefulWidget {
  const ScaleSheet({
    super.key,
    required this.name,
    required this.currentReplicas,
  });

  final String name;
  final int currentReplicas;

  @override
  State<ScaleSheet> createState() => _ScaleSheetState();
}

class _ScaleSheetState extends State<ScaleSheet> {
  late final TextEditingController _controller =
      TextEditingController(text: '${widget.currentReplicas}');
  String? _error;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _submit() {
    final text = _controller.text.trim();
    final parsed = int.tryParse(text);
    // FilteringTextInputFormatter.digitsOnly on the field makes negative
    // input unreachable through normal typing, so the only failure path
    // here is empty input (or an int.tryParse overflow on absurd lengths).
    if (parsed == null) {
      setState(() => _error = 'Enter a whole number');
      return;
    }
    Navigator.of(context).pop(parsed);
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final viewInsets = MediaQuery.of(context).viewInsets;
    return Padding(
      padding: EdgeInsets.only(bottom: viewInsets.bottom),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 16, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Center(
                child: Container(
                  width: 36,
                  height: 4,
                  margin: const EdgeInsets.only(bottom: 12),
                  decoration: BoxDecoration(
                    color: colors.borderSubtle,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
              Text(
                'Scale ${widget.name}',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 17,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                'Currently ${widget.currentReplicas} '
                '${widget.currentReplicas == 1 ? "replica" : "replicas"}.',
                style: TextStyle(color: colors.textSecondary, fontSize: 13),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: _controller,
                autofocus: true,
                keyboardType: TextInputType.number,
                inputFormatters: [
                  FilteringTextInputFormatter.digitsOnly,
                ],
                style: TextStyle(
                  fontFamily: 'monospace',
                  color: colors.textPrimary,
                ),
                onSubmitted: (_) => _submit(),
                decoration: InputDecoration(
                  labelText: 'Replicas',
                  errorText: _error,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(6),
                  ),
                ),
              ),
              const SizedBox(height: 20),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(),
                    style: TextButton.styleFrom(
                      foregroundColor: colors.textSecondary,
                    ),
                    child: const Text('Cancel'),
                  ),
                  const SizedBox(width: 8),
                  FilledButton(
                    onPressed: _submit,
                    style: FilledButton.styleFrom(
                      backgroundColor: colors.accent,
                      foregroundColor: Colors.white,
                    ),
                    child: const Text('Scale'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}
