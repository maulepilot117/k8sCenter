// Bottom sheet listing the 7 themes. Tapping a row sets the active theme
// via [themeControllerProvider] and pops.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../theme/kube_theme_builder.dart';
import '../../theme/theme_controller.dart';
import '../../theme/themes.g.dart';

class ThemePickerSheet extends ConsumerWidget {
  const ThemePickerSheet({super.key});

  static Future<void> show(BuildContext context) {
    return showModalBottomSheet(
      context: context,
      builder: (_) => const ThemePickerSheet(),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final activeId = ref.watch(themeControllerProvider);
    final colors = Theme.of(context).extension<KubeColors>()!;

    return SafeArea(
      child: RadioGroup<String>(
        groupValue: activeId,
        onChanged: (id) async {
          if (id == null) return;
          await ref.read(themeControllerProvider.notifier).setTheme(id);
          if (context.mounted) Navigator.of(context).pop();
        },
        child: ListView(
          shrinkWrap: true,
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Text(
                'Theme',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            for (final theme in kubeThemes.values)
              RadioListTile<String>(
                key: ValueKey('theme-radio-${theme.id}'),
                value: theme.id,
                title: Text(theme.name),
                activeColor: colors.accent,
              ),
          ],
        ),
      ),
    );
  }
}
