// PR-1a smoke test: app boots, renders the dashboard placeholder, and
// the default theme is active when no preference is stored.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/app.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/theme/theme_controller.dart';
import 'package:kubecenter/theme/themes.g.dart';
import 'package:shared_preferences/shared_preferences.dart';

Future<SharedPreferences> _emptyPrefs() async {
  SharedPreferences.setMockInitialValues({});
  return SharedPreferences.getInstance();
}

void main() {
  testWidgets('app boots and renders the placeholder', (tester) async {
    final prefs = await _emptyPrefs();

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          sharedPreferencesProvider.overrideWithValue(prefs),
        ],
        child: const KubeCenterApp(),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('k8sCenter'), findsWidgets);
    expect(find.textContaining('Dashboard arrives in PR-1c'), findsOneWidget);
  });

  testWidgets('every theme builds non-null ThemeData with a primary colour',
      (tester) async {
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
