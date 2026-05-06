// Smoke test that every theme builds non-null ThemeData. The full-app
// integration tests live in auth_repository_test, login_screen_test,
// adaptive_scaffold_test, and theme_picker_sheet_test — each covers a
// narrow slice without spinning up the routed app.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/theme/themes.g.dart';

void main() {
  test('every theme builds non-null ThemeData with a primary colour', () {
    for (final id in kubeThemes.keys) {
      final theme = buildKubeTheme(id);
      expect(theme.colorScheme.primary, isNotNull);
      expect(
        theme.extension<KubeColors>(),
        isNotNull,
        reason: 'theme $id should carry KubeColors extension',
      );
    }
  });
}
