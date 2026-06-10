// Active-theme id with SharedPreferences persistence. Only the root
// MaterialApp reads from this provider. Since the Liquid Glass redesign
// there is a single theme and no picker — the controller's remaining job
// is the graceful fallback for legacy multi-theme-era ids persisted on
// existing installs; setTheme() has no production callers until a second
// theme returns.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/shared_preferences_provider.dart';
import 'themes.g.dart';

// Re-exported so existing imports of
// `package:kubecenter/theme/theme_controller.dart' show sharedPreferencesProvider`
// keep working. New code should import directly from
// `providers/shared_preferences_provider.dart`.
export '../providers/shared_preferences_provider.dart'
    show sharedPreferencesProvider;

const String _prefsKey = 'kc_theme_id';

class ThemeController extends Notifier<String> {
  @override
  String build() {
    final prefs = ref.read(sharedPreferencesProvider);
    final stored = prefs.getString(_prefsKey);
    if (stored != null && kubeThemes.containsKey(stored)) {
      return stored;
    }
    return defaultThemeId;
  }

  /// Sets the active theme. No-op when [id] is unknown.
  Future<void> setTheme(String id) async {
    if (!kubeThemes.containsKey(id)) return;
    state = id;
    final prefs = ref.read(sharedPreferencesProvider);
    await prefs.setString(_prefsKey, id);
  }
}

final themeControllerProvider = NotifierProvider<ThemeController, String>(
  ThemeController.new,
);
