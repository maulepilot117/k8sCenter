// Modal bottom-sheet confirmation dialog. Mirrors the contract of
// `frontend/components/ui/ConfirmDialog.tsx`.
//
// Usage:
//   final ok = await showConfirmSheet(
//     context: context,
//     title: 'Delete pod',
//     message: 'This will permanently delete "my-pod".',
//     confirmLabel: 'Delete',
//     danger: true,
//     typeToConfirm: 'my-pod',
//   );
//   if (ok == true) { ... }
//
// When [typeToConfirm] is non-null, the confirm button stays disabled
// until the input matches (after trim) — same gating as ConfirmDialog's
// `canConfirm = !typeToConfirm || input.value === typeToConfirm`.

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';

/// Open the confirm sheet. Returns true on confirm, false on cancel,
/// null on dismiss-by-scrim.
Future<bool?> showConfirmSheet({
  required BuildContext context,
  required String title,
  String? message,
  required String confirmLabel,
  bool danger = false,
  String? typeToConfirm,
}) {
  return showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    builder: (ctx) => ConfirmSheet(
      title: title,
      message: message,
      confirmLabel: confirmLabel,
      danger: danger,
      typeToConfirm: typeToConfirm,
    ),
  );
}

class ConfirmSheet extends StatefulWidget {
  const ConfirmSheet({
    super.key,
    required this.title,
    required this.confirmLabel,
    this.message,
    this.danger = false,
    this.typeToConfirm,
  });

  final String title;
  final String? message;
  final String confirmLabel;
  final bool danger;

  /// When set, an input field renders and the confirm button stays
  /// disabled until input.trim() matches this string (case-sensitive).
  final String? typeToConfirm;

  @override
  State<ConfirmSheet> createState() => _ConfirmSheetState();
}

class _ConfirmSheetState extends State<ConfirmSheet> {
  late final TextEditingController _controller = TextEditingController()
    ..addListener(() => setState(() {}));

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  bool get _canConfirm {
    final required = widget.typeToConfirm;
    if (required == null) return true;
    return _normalize(_controller.text) == _normalize(required);
  }

  /// Trim whitespace (covers autocorrect's trailing space on iOS) and strip
  /// zero-width characters that arrive via clipboard pastes from rich-text
  /// surfaces (Slack, browser, mail clients). Without this, a paste that
  /// looks identical to the operator silently fails the equality check.
  ///
  /// The regex uses `\uXXXX` escape sequences rather than embedded literal
  /// zero-width characters because some editors and CI tools (Unicode-
  /// normalizing dart format passes, certain git filters, paste-through-
  /// markdown rendering) silently strip the literal codepoints from the
  /// source, collapsing the regex to an empty character class and breaking
  /// normalization invisibly. Escapes survive every text transform.
  static String _normalize(String s) {
    final trimmed = s.trim();
    // U+200B ZWSP, U+200C ZWNJ, U+200D ZWJ, U+FEFF BOM.
    return trimmed.replaceAll(RegExp('[\\u200B-\\u200D\\uFEFF]'), '');
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final viewInsets = MediaQuery.of(context).viewInsets;
    final accent = widget.danger ? colors.error : colors.accent;
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
              // Drag handle.
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
                widget.title,
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 17,
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (widget.message != null) ...[
                const SizedBox(height: 8),
                Text(
                  widget.message!,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 14,
                    height: 1.4,
                  ),
                ),
              ],
              if (widget.typeToConfirm != null) ...[
                const SizedBox(height: 16),
                Text.rich(
                  TextSpan(
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 13,
                    ),
                    children: [
                      const TextSpan(text: 'Type '),
                      TextSpan(
                        text: widget.typeToConfirm,
                        style: const TextStyle(
                          fontFamily: 'monospace',
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const TextSpan(text: ' to confirm'),
                    ],
                  ),
                ),
                const SizedBox(height: 8),
                TextField(
                  controller: _controller,
                  autofocus: true,
                  autocorrect: false,
                  enableSuggestions: false,
                  textCapitalization: TextCapitalization.none,
                  // Opt out of contextual autofill so a password manager
                  // that indexed the resource name (e.g. as "username") cannot
                  // one-tap satisfy the destructive-verb friction gate. The
                  // gate exists to force operator-conscious confirmation;
                  // autofill defeats that.
                  autofillHints: const <String>[],
                  style: TextStyle(
                    fontFamily: 'monospace',
                    color: colors.textPrimary,
                  ),
                  decoration: InputDecoration(
                    hintText: widget.typeToConfirm,
                    hintStyle: TextStyle(
                      color: colors.textMuted,
                      fontFamily: 'monospace',
                    ),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(6),
                      borderSide: BorderSide(color: colors.borderSubtle),
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(6),
                      borderSide: BorderSide(color: colors.borderSubtle),
                    ),
                    focusedBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(6),
                      borderSide: BorderSide(color: accent),
                    ),
                  ),
                ),
              ],
              const SizedBox(height: 20),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(false),
                    style: TextButton.styleFrom(
                      foregroundColor: colors.textSecondary,
                    ),
                    child: const Text('Cancel'),
                  ),
                  const SizedBox(width: 8),
                  FilledButton(
                    onPressed: _canConfirm
                        ? () => Navigator.of(context).pop(true)
                        : null,
                    style: FilledButton.styleFrom(
                      backgroundColor: accent,
                      foregroundColor: Colors.white,
                    ),
                    child: Text(widget.confirmLabel),
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
