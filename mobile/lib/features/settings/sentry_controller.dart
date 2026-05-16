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
import '../../providers/shared_preferences_provider.dart';

class SentryController extends Notifier<bool> {
  /// Serializes concurrent setOptIn calls so a rapid double-tap can't
  /// interleave Sentry.init with Sentry.close (which can crash the
  /// underlying SDK transport mid-handshake). The second call waits for
  /// the first to fully resolve before proceeding.
  Future<void>? _inflight;

  @override
  bool build() {
    final prefs = ref.read(sharedPreferencesProvider);
    return prefs.getBool(kSentryOptInPrefsKey) ?? false;
  }

  /// Toggles opt-in. Idempotent — calling with the current state is a
  /// no-op. Concurrent calls are serialized via [_inflight].
  Future<void> setOptIn(bool optIn) async {
    // Drain any prior toggle before evaluating the no-op fast path:
    // otherwise we'd compare against stale `state` mid-transition.
    final prior = _inflight;
    if (prior != null) {
      await prior;
    }
    if (state == optIn) return;

    final pending = _doSetOptIn(optIn);
    _inflight = pending;
    try {
      await pending;
    } finally {
      if (identical(_inflight, pending)) {
        _inflight = null;
      }
    }
  }

  Future<void> _doSetOptIn(bool optIn) async {
    final prefs = ref.read(sharedPreferencesProvider);
    await prefs.setBool(kSentryOptInPrefsKey, optIn);
    state = optIn;

    if (optIn) {
      // Init may return false if no DSN was wired into the build —
      // that's OK; the preference is still on so subsequent builds-with-
      // DSN will pick it up at boot.
      await initSentryIfOptedIn(prefs);
    } else {
      await closeSentry();
    }
  }
}

final sentryControllerProvider =
    NotifierProvider<SentryController, bool>(SentryController.new);
