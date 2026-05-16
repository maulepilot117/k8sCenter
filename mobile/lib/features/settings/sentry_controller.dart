// Settings → Crash reporting toggle state.
//
// Reads from / writes to the `sentry_opt_in` shared_preferences key. The
// boot-time `initSentryIfOptedIn` read happens before runApp; this
// controller is the runtime mutation surface for the Settings toggle.
//
// Toggle behaviour:
//   - false → true: calls [initSentryIfOptedIn] so events flow without
//     requiring an app restart.
//   - true → false: calls [closeSentry] so any in-flight events are
//     flushed and the transport shuts down.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../observability/sentry_init.dart';
import '../../theme/theme_controller.dart' show sharedPreferencesProvider;

class SentryController extends Notifier<bool> {
  @override
  bool build() {
    final prefs = ref.read(sharedPreferencesProvider);
    return prefs.getBool(kSentryOptInPrefsKey) ?? false;
  }

  /// Toggles opt-in. Idempotent — calling with the current state is a
  /// no-op.
  Future<void> setOptIn(bool optIn) async {
    if (state == optIn) return;

    final prefs = ref.read(sharedPreferencesProvider);
    await prefs.setBool(kSentryOptInPrefsKey, optIn);
    state = optIn;

    if (optIn) {
      // Init may return false if no DSN was wired into the build —
      // that's OK; the preference is still on so subsequent builds-with-
      // DSN will pick it up at boot.
      await initSentryIfOptedIn();
    } else {
      await closeSentry();
    }
  }
}

final sentryControllerProvider =
    NotifierProvider<SentryController, bool>(SentryController.new);
