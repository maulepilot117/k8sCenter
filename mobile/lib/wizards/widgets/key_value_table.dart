// Repeating key-value rows. Used by ConfigMap, Secret, and (later)
// untyped credential forms for SecretStore providers.
//
// Stateless — the parent owns the list of pairs and receives back the
// edited list via [onChanged]. This makes the widget cheap to compose
// and lets the wizard controller hold the canonical form state.
//
// UX decisions worth noting:
//   * A trailing empty row is always rendered so the operator can add
//     without tapping a "+ Add" button. When they fill it, a new
//     trailing empty row appears.
//   * Removing a row is one tap on the trailing icon — no confirm
//     dialog. Undoing is just retyping the value, so the friction
//     isn't worth a sheet.
//   * Duplicate keys are *not* prevented at the widget level — the
//     wizard's local validator can flag them, and the backend rejects
//     them on preview.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

class KeyValuePair {
  const KeyValuePair({this.key = '', this.value = ''});

  final String key;
  final String value;

  KeyValuePair copyWith({String? key, String? value}) =>
      KeyValuePair(key: key ?? this.key, value: value ?? this.value);

  bool get isEmpty => key.isEmpty && value.isEmpty;
}

class KeyValueTable extends StatefulWidget {
  const KeyValueTable({
    super.key,
    required this.pairs,
    required this.onChanged,
    this.keyLabel = 'Key',
    this.valueLabel = 'Value',
    this.valueIsObscured = false,
    this.errorMessage,
  });

  final List<KeyValuePair> pairs;
  final ValueChanged<List<KeyValuePair>> onChanged;
  final String keyLabel;
  final String valueLabel;

  /// True for Secret rows — values render as obscured text fields.
  final bool valueIsObscured;

  /// Optional inline error rendered under the table (e.g., from
  /// preview-time field-level validation).
  final String? errorMessage;

  @override
  State<KeyValueTable> createState() => _KeyValueTableState();
}

class _KeyValueTableState extends State<KeyValueTable> {
  /// Each row owns two TextEditingControllers so cursor position and
  /// selection survive parent rebuilds. We rebuild controllers only
  /// when the parent supplies a different number of rows or the row
  /// content diverges from what the controller already shows (the
  /// latter handles initial load and external resets).
  late List<_RowControllers> _controllers;

  @override
  void initState() {
    super.initState();
    _controllers = _buildControllers(_displayPairs(widget.pairs));
  }

  @override
  void didUpdateWidget(covariant KeyValueTable oldWidget) {
    super.didUpdateWidget(oldWidget);
    final next = _displayPairs(widget.pairs);
    if (next.length != _controllers.length) {
      _disposeAll();
      _controllers = _buildControllers(next);
      return;
    }
    for (var i = 0; i < next.length; i++) {
      final c = _controllers[i];
      if (c.key.text != next[i].key) c.key.text = next[i].key;
      if (c.value.text != next[i].value) c.value.text = next[i].value;
    }
  }

  @override
  void dispose() {
    _disposeAll();
    super.dispose();
  }

  void _disposeAll() {
    for (final c in _controllers) {
      c.key.dispose();
      c.value.dispose();
    }
  }

  /// Returns the pair list with a trailing empty row if the last row
  /// isn't already empty. Keeps the "always-typeable next row" UX
  /// without forcing the parent to manage a sentinel.
  List<KeyValuePair> _displayPairs(List<KeyValuePair> pairs) {
    if (pairs.isEmpty || !pairs.last.isEmpty) {
      return [...pairs, const KeyValuePair()];
    }
    return pairs;
  }

  List<_RowControllers> _buildControllers(List<KeyValuePair> pairs) {
    return pairs
        .map((p) => _RowControllers(
              key: TextEditingController(text: p.key),
              value: TextEditingController(text: p.value),
            ))
        .toList();
  }

  void _emit(List<KeyValuePair> rows) {
    // Strip trailing empty rows before bubbling up so the parent's
    // canonical state stays clean (no perpetually-empty sentinel).
    final cleaned = [...rows];
    while (cleaned.isNotEmpty && cleaned.last.isEmpty) {
      cleaned.removeLast();
    }
    widget.onChanged(cleaned);
  }

  void _onKeyChanged(int i, String text) {
    final rows = _readRows();
    rows[i] = rows[i].copyWith(key: text);
    _emit(rows);
  }

  void _onValueChanged(int i, String text) {
    final rows = _readRows();
    rows[i] = rows[i].copyWith(value: text);
    _emit(rows);
  }

  void _onRemoveRow(int i) {
    final rows = _readRows()..removeAt(i);
    _emit(rows);
  }

  List<KeyValuePair> _readRows() {
    return [
      for (final c in _controllers)
        KeyValuePair(key: c.key.text, value: c.value.text),
    ];
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (var i = 0; i < _controllers.length; i++)
          Padding(
            padding: EdgeInsets.only(
                bottom: i == _controllers.length - 1 ? 0 : 8),
            child: Row(
              children: [
                Expanded(
                  flex: 4,
                  child: TextField(
                    controller: _controllers[i].key,
                    onChanged: (text) => _onKeyChanged(i, text),
                    decoration: InputDecoration(
                      labelText: i == 0 ? widget.keyLabel : null,
                      hintText: widget.keyLabel,
                      isDense: true,
                      border: const OutlineInputBorder(),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  flex: 6,
                  child: TextField(
                    controller: _controllers[i].value,
                    onChanged: (text) => _onValueChanged(i, text),
                    obscureText: widget.valueIsObscured,
                    decoration: InputDecoration(
                      labelText: i == 0 ? widget.valueLabel : null,
                      hintText: widget.valueLabel,
                      isDense: true,
                      border: const OutlineInputBorder(),
                    ),
                  ),
                ),
                IconButton(
                  visualDensity: VisualDensity.compact,
                  tooltip: 'Remove row',
                  // The trailing empty sentinel row has no remove button —
                  // there's nothing to remove.
                  onPressed: i == _controllers.length - 1 &&
                          _controllers[i].key.text.isEmpty &&
                          _controllers[i].value.text.isEmpty
                      ? null
                      : () => _onRemoveRow(i),
                  icon: Icon(Icons.close, color: colors.textMuted, size: 18),
                ),
              ],
            ),
          ),
        if (widget.errorMessage != null) ...[
          const SizedBox(height: 8),
          Text(
            widget.errorMessage!,
            style: TextStyle(color: colors.error, fontSize: 12),
          ),
        ],
      ],
    );
  }
}

class _RowControllers {
  _RowControllers({required this.key, required this.value});
  final TextEditingController key;
  final TextEditingController value;
}
