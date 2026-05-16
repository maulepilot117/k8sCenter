// Riverpod provider holding the hydrated SharedPreferences singleton.
//
// `main.dart` calls `SharedPreferences.getInstance()` once at startup and
// installs the result via `overrideWithValue` on the root container.
// Synchronous downstream readers (`ThemeController.build`,
// `SentryController.build`) read it directly instead of awaiting a
// per-call platform-channel round-trip. Tests use the same override
// shape with a mock-initialized SharedPreferences.
//
// Lives here (not under `theme/`) so non-theme features can depend on it
// without inheriting a theme-package dependency edge.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

final sharedPreferencesProvider = Provider<SharedPreferences>((ref) {
  throw UnimplementedError(
    'sharedPreferencesProvider must be overridden in main() and tests',
  );
});
