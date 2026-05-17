// First-launch onboarding completion flag.
//
// Backed by the `onboarded_v1` shared_preferences key. Two callers:
//   1. Router redirect (app_router.dart) — reads the flag synchronously
//      to decide whether to send a not-yet-authenticated visitor to
//      `/onboarding` or `/login`.
//   2. Onboarding screen "Get started" / "Skip" buttons — flip the flag
//      to true so the next launch routes straight to `/login`.
//
// Internal-beta upgrade detection runs once during main() bootstrap:
// see [migrateOnboardingFlagForUpgrade]. After that resolves, the flag
// reliably distinguishes "fresh install" from "upgrade from beta", so
// the router redirect can stay synchronous (no async keychain read on
// every navigation).

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../auth/secure_storage.dart';
import '../../providers/shared_preferences_provider.dart';

/// Versioned key — bumping the suffix lets us re-show the tour after a
/// material refresh (e.g. when a future milestone adds a biometric card).
const String kOnboardedPrefsKey = 'onboarded_v1';

class OnboardingController extends Notifier<bool> {
  @override
  bool build() {
    final prefs = ref.read(sharedPreferencesProvider);
    return prefs.getBool(kOnboardedPrefsKey) ?? false;
  }

  /// Persists completion. Called on Skip / Get-started taps. Idempotent
  /// when already true.
  Future<void> complete() async {
    if (state) return;
    final prefs = ref.read(sharedPreferencesProvider);
    await prefs.setBool(kOnboardedPrefsKey, true);
    state = true;
  }
}

final onboardingControllerProvider =
    NotifierProvider<OnboardingController, bool>(OnboardingController.new);

/// Internal-beta upgrade detection. Run once before runApp so the
/// router redirect sees a final flag value and doesn't race with an
/// async refresh-token read.
///
/// If [kOnboardedPrefsKey] is absent AND a refresh token survives in
/// secure storage, the device is an internal-beta upgrade: silently
/// set the flag. Otherwise (fresh install, no token), leave it absent
/// so the router sends the user to `/onboarding`.
///
/// The plan considered an explicit `app_version_first_run` flag for
/// this; the refresh-token proxy needs no migration scaffolding and
/// works regardless of which build first introduced the flag.
Future<void> migrateOnboardingFlagForUpgrade(
  SharedPreferences prefs,
  SecureTokenStore tokenStore,
) async {
  if (prefs.containsKey(kOnboardedPrefsKey)) return;
  final refreshToken = await tokenStore.readRefreshToken();
  if (refreshToken == null) return;
  await prefs.setBool(kOnboardedPrefsKey, true);
}
