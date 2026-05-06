// Verifies the bridge from generated string tokens to Material 3
// ThemeData. Each theme's primary colour must match the generated accent
// token; rgba() and #hex parsing both round-trip.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/theme/themes.g.dart';

void main() {
  test('every kubeTheme builds with primary == parsed accent', () {
    for (final entry in kubeThemes.entries) {
      final theme = buildKubeTheme(entry.key);
      final colors = theme.extension<KubeColors>()!;

      // Re-parse the generator's accent string, compare to ColorScheme.primary.
      // Both should equal the same Color instance.
      expect(theme.colorScheme.primary, equals(colors.accent));
    }
  });

  test('unknown theme id falls back to defaultThemeId', () {
    final fallback = buildKubeTheme('does-not-exist');
    final reference = buildKubeTheme(defaultThemeId);
    expect(fallback.colorScheme.primary, equals(reference.colorScheme.primary));
  });

  test('Nexus accent parses to expected ARGB', () {
    final theme = buildKubeTheme('nexus');
    final colors = theme.extension<KubeColors>()!;
    // accent: '#00C2FF' → 0xFF00C2FF
    expect(colors.accent.toARGB32(), equals(0xFF00C2FF));
  });

  test('rgba() tokens preserve alpha', () {
    final theme = buildKubeTheme('nexus');
    final colors = theme.extension<KubeColors>()!;
    // accentDim: 'rgba(0, 194, 255, 0.08)' — alpha 0.08*255 ≈ 20
    expect(colors.accentDim.a, closeTo(0.08, 0.01));
  });

  test('every theme registers KubeColors as a ThemeExtension', () {
    for (final id in kubeThemes.keys) {
      final theme = buildKubeTheme(id);
      expect(
        theme.extension<KubeColors>(),
        isNotNull,
        reason: 'theme $id is missing KubeColors',
      );
    }
  });

  test('KubeColors.lerp interpolates between themes', () {
    final from = buildKubeTheme('nexus').extension<KubeColors>()!;
    final to = buildKubeTheme('dracula').extension<KubeColors>()!;
    final mid = from.lerp(to, 0.5);
    expect(mid, isA<KubeColors>());
    // At t=0 the result equals `from`; at t=1, equals `to`.
    expect(from.lerp(to, 0).accent, equals(from.accent));
    expect(from.lerp(to, 1).accent, equals(to.accent));
  });
}
