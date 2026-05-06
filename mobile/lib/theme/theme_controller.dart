// Active-theme state with SharedPreferences persistence. Survives cold
// starts. Theme picker and the root MaterialApp both read from this
// provider — picker writes, MaterialApp watches.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'themes.g.dart';

const String _prefsKey = 'kc_theme_id';

/// SharedPreferences instance, initialized once at app start. Override in
/// widget tests with `ProviderScope(overrides: [sharedPreferencesProvider
/// .overrideWithValue(await SharedPreferences.getInstance())])`.
final sharedPreferencesProvider = Provider<SharedPreferences>((ref) {
  throw UnimplementedError(
    'sharedPreferencesProvider must be overridden in main() and tests',
  );
});

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
