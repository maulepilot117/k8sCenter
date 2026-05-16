// Active-theme state with SharedPreferences persistence. Survives cold
// starts. Theme picker and the root MaterialApp both read from this
// provider — picker writes, MaterialApp watches.

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
