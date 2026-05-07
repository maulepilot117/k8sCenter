// Edit-mode toggle for the YAML tab. Wraps the read-only SelectableText
// with a TextField backed by [YamlApplyController]. M2 ships without
// syntax highlighting (no code_text_field dep) — operators editing a
// ConfigMap or Secret on a phone get a functional plain-text editor;
// fancy highlighting is a future polish if the operator demand surfaces.
//
// State machine:
//   - Read-only view: shows the resource as JSON-pretty-printed text.
//   - Edit mode: TextField over a YamlApplyController-backed buffer.
//     Validate/Apply hit the controller; result panel renders below.
//   - After Apply success: result panel + "Done" button returns to
//     read-only view (the underlying resource has been re-fetched by
//     the controller's invalidate so the read-only YAML reflects the
//     new state).

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/yaml_apply_controller.dart';
import '../theme/kube_theme_builder.dart';

class YamlEditorPanel extends ConsumerStatefulWidget {
  const YamlEditorPanel({
    super.key,
    required this.applyKey,
    required this.resource,
    this.headerWarning,
    this.stripSensitiveDataFields = false,
  });

  final YamlApplyKey applyKey;
  final Map<String, dynamic> resource;

  /// Optional banner above the editor — Secret detail uses this to
  /// remind operators that `data` values are base64.
  final Widget? headerWarning;

  /// **Critical for Secrets:** the GET response masks `data` /
  /// `stringData` values to the literal string `"****"`. Without
  /// stripping these fields from the editor seed, an operator who edits
  /// a label and applies will SSA-write `"****"` over every credential
  /// entry — destroying the actual secret data. Set this true for
  /// Secrets so the editor seed omits those fields entirely; SSA then
  /// leaves them untouched.
  final bool stripSensitiveDataFields;

  @override
  ConsumerState<YamlEditorPanel> createState() => _YamlEditorPanelState();
}

class _YamlEditorPanelState extends ConsumerState<YamlEditorPanel> {
  bool _editing = false;
  TextEditingController? _textController;

  String get _initialText {
    final source = widget.stripSensitiveDataFields
        ? _withoutSensitiveData(widget.resource)
        : widget.resource;
    return const JsonEncoder.withIndent('  ').convert(source);
  }

  /// Returns a shallow copy of [input] with `data` and `stringData`
  /// removed. The Secret GET response carries those fields with values
  /// already masked to `"****"` by the backend; including them in the
  /// editor seed means SSA would persist the mask back over real
  /// credential bytes. Omitting them entirely lets SSA leave existing
  /// values untouched while the operator edits anything else.
  static Map<String, dynamic> _withoutSensitiveData(
      Map<String, dynamic> input) {
    final out = <String, dynamic>{};
    for (final entry in input.entries) {
      if (entry.key == 'data' || entry.key == 'stringData') continue;
      out[entry.key] = entry.value;
    }
    return out;
  }

  void _enterEditMode() {
    final text = _initialText;
    _textController?.dispose();
    _textController = TextEditingController(text: text);
    ref.read(yamlApplyControllerProvider(widget.applyKey).notifier)
        .setContent(text);
    setState(() => _editing = true);
  }

  void _exitEditMode() {
    _textController?.dispose();
    _textController = null;
    ref
        .read(yamlApplyControllerProvider(widget.applyKey).notifier)
        .reset();
    setState(() => _editing = false);
  }

  @override
  void dispose() {
    _textController?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    if (!_editing) {
      return _ReadOnlyView(
        resource: widget.stripSensitiveDataFields
            ? _withoutSensitiveData(widget.resource)
            : widget.resource,
        headerWarning: widget.headerWarning,
        onEdit: _enterEditMode,
      );
    }
    final state = ref.watch(yamlApplyControllerProvider(widget.applyKey));
    final controller =
        ref.read(yamlApplyControllerProvider(widget.applyKey).notifier);
    final busy = state.status == YamlApplyStatus.validating ||
        state.status == YamlApplyStatus.applying;

    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ?widget.headerWarning,
          if (state.error != null)
            Container(
              margin: const EdgeInsets.only(bottom: 12),
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: colors.errorDim,
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: colors.error),
              ),
              child: Row(
                children: [
                  Icon(Icons.error_outline, color: colors.error, size: 18),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      state.error!,
                      style: TextStyle(color: colors.error, fontSize: 13),
                    ),
                  ),
                ],
              ),
            ),
          TextField(
            controller: _textController,
            maxLines: null,
            minLines: 16,
            style: const TextStyle(
              fontFamily: 'monospace',
              fontSize: 12,
              height: 1.4,
            ),
            keyboardType: TextInputType.multiline,
            autocorrect: false,
            enableSuggestions: false,
            textCapitalization: TextCapitalization.none,
            inputFormatters: const <TextInputFormatter>[],
            onChanged: controller.setContent,
            decoration: InputDecoration(
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
                borderSide: BorderSide(color: colors.accent),
              ),
            ),
          ),
          const SizedBox(height: 12),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            alignment: WrapAlignment.end,
            children: [
              TextButton(
                onPressed: busy ? null : _exitEditMode,
                style: TextButton.styleFrom(
                  foregroundColor: colors.textSecondary,
                ),
                child: const Text('Cancel'),
              ),
              OutlinedButton(
                onPressed: busy ? null : controller.validate,
                child: state.status == YamlApplyStatus.validating
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Validate'),
              ),
              FilledButton(
                onPressed: busy ? null : controller.apply,
                style: FilledButton.styleFrom(
                  backgroundColor: colors.accent,
                  foregroundColor: Colors.white,
                ),
                child: state.status == YamlApplyStatus.applying
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : const Text('Apply'),
              ),
            ],
          ),
          if (state.result != null) ...[
            const SizedBox(height: 16),
            _ResultPanel(
              status: state.status,
              result: state.result!,
              onDone: _exitEditMode,
            ),
          ],
        ],
      ),
    );
  }
}

class _ReadOnlyView extends StatelessWidget {
  const _ReadOnlyView({
    required this.resource,
    required this.onEdit,
    this.headerWarning,
  });

  final Map<String, dynamic> resource;
  final VoidCallback onEdit;
  final Widget? headerWarning;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final pretty = const JsonEncoder.withIndent('  ').convert(resource);
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ?headerWarning,
          Align(
            alignment: Alignment.centerRight,
            child: OutlinedButton.icon(
              onPressed: onEdit,
              icon: const Icon(Icons.edit_outlined, size: 16),
              label: const Text('Edit'),
            ),
          ),
          const SizedBox(height: 8),
          SelectableText(
            pretty,
            style: TextStyle(
              fontFamily: 'monospace',
              fontSize: 12,
              color: colors.textPrimary,
              height: 1.4,
            ),
          ),
        ],
      ),
    );
  }
}

class _ResultPanel extends StatelessWidget {
  const _ResultPanel({
    required this.status,
    required this.result,
    required this.onDone,
  });

  final YamlApplyStatus status;
  final ApplyResponse result;
  final VoidCallback onDone;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final summary = result.summary;
    final isApplied = status == YamlApplyStatus.applied;
    final headerLabel = isApplied ? 'Apply complete' : 'Dry run';
    final tone = isApplied ? colors.success : colors.accent;

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: tone.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: tone.withValues(alpha: 0.4)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                isApplied ? Icons.check_circle_outline : Icons.preview_outlined,
                color: tone,
                size: 18,
              ),
              const SizedBox(width: 8),
              Text(
                headerLabel,
                style: TextStyle(
                  color: colors.textPrimary,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Spacer(),
              Text(
                '${summary.total} resources',
                style: TextStyle(color: colors.textSecondary, fontSize: 12),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 4,
            children: [
              if (summary.created > 0)
                _SummaryChip(label: '${summary.created} created', color: tone),
              if (summary.configured > 0)
                _SummaryChip(
                    label: '${summary.configured} configured', color: tone),
              if (summary.unchanged > 0)
                _SummaryChip(
                    label: '${summary.unchanged} unchanged',
                    color: colors.textMuted),
              if (summary.failed > 0)
                _SummaryChip(
                    label: '${summary.failed} failed', color: colors.error),
            ],
          ),
          if (result.results.any((r) => r.error != null)) ...[
            const Divider(height: 24),
            for (final r in result.results.where((r) => r.error != null))
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Text(
                  '${r.kind}/${r.name}: ${r.error}',
                  style: TextStyle(color: colors.error, fontSize: 12),
                ),
              ),
          ],
          if (isApplied) ...[
            const SizedBox(height: 12),
            Align(
              alignment: Alignment.centerRight,
              child: TextButton(
                onPressed: onDone,
                child: const Text('Done'),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _SummaryChip extends StatelessWidget {
  const _SummaryChip({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}
