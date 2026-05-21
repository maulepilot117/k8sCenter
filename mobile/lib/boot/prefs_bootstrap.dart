// Cold-start SharedPreferences hydration with an upper time bound.
//
// Closes #270 — on Android in the Before-First-Unlock state (post-reboot,
// pre-PIN entry), encrypted-storage-backed SharedPreferences can hang
// indefinitely. `main.dart` previously did an unbounded `await
// SharedPreferences.getInstance()` before `runApp`, so the app would stay
// on a blank window for as long as storage stayed offline. This helper
// caps the wait at [kDefaultPrefsHydrationTimeout] and falls back to an
// in-memory store so boot can complete with defaults.
//
// Fallback trade-off: once the in-memory store is installed (via
// `SharedPreferences.setMockInitialValues({})`), the user's persisted
// theme + Sentry opt-in are unreachable for the rest of the process
// lifetime. Acceptable failure mode — the app remains usable, defaults
// are sensible (default theme, Sentry off), and a subsequent cold start
// with the real backing online recovers the persisted state. Documented
// here, not surfaced to the user via toast: a Before-First-Unlock boot
// already implies the user hasn't completed device unlock yet, so a
// "couldn't read preferences" message would compete with the system
// PIN entry UI.

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Default upper bound on `SharedPreferences.getInstance()` during cold
/// start. 10s chosen to absorb legitimate slow first-launches on low-end
/// Android devices where encrypted-storage init can take 3-8s, while
/// still cutting off the unbounded hang in Before-First-Unlock. No
/// telemetry path exists at this point in boot — Sentry initialization
/// is downstream of this call — so the value is a defensive default
/// rather than a measured one. Retune if real-device data shows the
/// timeout firing on legitimate slow boots.
const Duration kDefaultPrefsHydrationTimeout = Duration(seconds: 10);

/// Hydrates [SharedPreferences] with a [timeout] upper bound. On timeout,
/// installs an empty in-memory store via
/// `SharedPreferences.setMockInitialValues({})` (which resets the
/// package's cached completer so the next `getInstance()` resolves
/// against the in-memory backing rather than the still-hung platform
/// channel) and returns the resulting [SharedPreferences].
///
/// [loadPrefs] defaults to `SharedPreferences.getInstance` and is
/// overridable in tests so the timeout/fallback branches can be
/// exercised without mocking the platform channel directly.
Future<SharedPreferences> hydratePrefsWithTimeout({
  Future<SharedPreferences> Function()? loadPrefs,
  Duration timeout = kDefaultPrefsHydrationTimeout,
}) async {
  final load = loadPrefs ?? SharedPreferences.getInstance;
  try {
    return await load().timeout(timeout);
  } on TimeoutException catch (error) {
    debugPrint(
      'SharedPreferences.getInstance() exceeded ${timeout.inSeconds}s '
      '— booting with in-memory prefs fallback. $error',
    );
    // `setMockInitialValues` is the package's documented escape hatch
    // for installing an `InMemorySharedPreferencesStore`. Marked
    // `@visibleForTesting` but used here at production runtime because
    // the alternative (constructing an in-memory store via
    // shared_preferences_platform_interface) pulls in a transitive dep
    // for one fallback path. The reset of the internal completer makes
    // the subsequent `getInstance()` call safe even when the original
    // platform-channel future is still pending.
    // ignore: invalid_use_of_visible_for_testing_member
    SharedPreferences.setMockInitialValues(const <String, Object>{});
    return SharedPreferences.getInstance();
  }
}
