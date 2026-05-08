// Read-only YAML preview rendered on the Review step.
//
// M2 deliberately ships without `code_text_field` syntax highlighting
// (see `mobile/lib/widgets/yaml_editor_panel.dart`'s top comment); M3
// inherits the decision so the wizard preview matches the direct-edit
// experience operators already know. SelectableText with monospace
// styling and a soft-wrapped scroll container is enough — operators can
// long-press to copy when they want to dump the YAML elsewhere.
//
// On loading, renders a centered spinner. On failure, renders an error
// banner with a Retry callback so the operator can re-tap without
// reaching into the form steps.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';

class YamlPreviewPanel extends StatelessWidget {
  const YamlPreviewPanel({
    super.key,
    required this.yaml,
    this.loading = false,
    this.errorMessage,
    this.onRetry,
  });

  final String yaml;
  final bool loading;
  final String? errorMessage;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;

    if (loading) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 32),
        child: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              CircularProgressIndicator(color: colors.accent),
              const SizedBox(height: 12),
              Text(
                'Generating preview…',
                style: TextStyle(color: colors.textSecondary, fontSize: 13),
              ),
            ],
          ),
        ),
      );
    }

    if (errorMessage != null) {
      return Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: colors.error.withValues(alpha: 0.08),
          border: Border.all(color: colors.error.withValues(alpha: 0.4)),
          borderRadius: BorderRadius.circular(8),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.error_outline, color: colors.error, size: 20),
                const SizedBox(width: 8),
                Text(
                  'Preview failed',
                  style: TextStyle(
                    color: colors.error,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              errorMessage!,
              style: TextStyle(color: colors.textPrimary, fontSize: 13),
            ),
            if (onRetry != null) ...[
              const SizedBox(height: 12),
              Align(
                alignment: Alignment.centerRight,
                child: TextButton.icon(
                  onPressed: onRetry,
                  icon: const Icon(Icons.refresh, size: 18),
                  label: const Text('Retry'),
                  style: TextButton.styleFrom(foregroundColor: colors.error),
                ),
              ),
            ],
          ],
        ),
      );
    }

    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        border: Border.all(color: colors.borderSubtle),
        borderRadius: BorderRadius.circular(8),
      ),
      child: SelectableText(
        yaml.isEmpty ? '# (empty preview)' : yaml,
        style: TextStyle(
          fontFamily: 'monospace',
          fontSize: 12,
          height: 1.45,
          color: colors.textPrimary,
        ),
      ),
    );
  }
}
