// Verifies hydratePrefsWithTimeout's branches:
//   1. Happy path: load resolves within timeout → returns its value.
//   2. Timeout: load never resolves → falls back to empty in-memory
//      SharedPreferences and boot continues.
//   3. Late-resolving load: an orphaned platform future doesn't pollute
//      the returned fallback instance.
//   4. Non-timeout Exception (PlatformException, MissingPluginException,
//      etc.) also falls back so boot is guaranteed a usable handle.
//   5. The fallback sets a BFU sentinel for the post-Sentry-init
//      breadcrumb path.

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/providers/prefs_bootstrap.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('returns the loader\'s value when it resolves within timeout',
      () async {
    SharedPreferences.setMockInitialValues({'kc_theme_id': 'graphite'});
    final prefs = await hydratePrefsWithTimeout(
      loadPrefs: SharedPreferences.getInstance,
      timeout: const Duration(seconds: 5),
    );
    expect(prefs.getString('kc_theme_id'), 'graphite');
  });

  test('falls back to empty in-memory prefs on timeout', () async {
    SharedPreferences.setMockInitialValues({'kc_theme_id': 'graphite'});
    final fallback = await hydratePrefsWithTimeout(
      // Loader never completes — exercise the timeout branch.
      loadPrefs: () => Completer<SharedPreferences>().future,
      timeout: const Duration(milliseconds: 50),
    );
    // The fallback installs an empty in-memory store, so the
    // pre-existing mock value is gone — proves we're on the fallback
    // path, not the original mock that was in setUp(). The internal
    // BFU sentinel is set by design (see separate test); only
    // user-facing keys should be absent.
    expect(fallback.getString('kc_theme_id'), isNull);
    expect(
      fallback.getKeys().where((k) => k != kPrefsBfuFallbackSentinelKey),
      isEmpty,
    );
  });

  test('fallback prefs are writable so boot consumers can persist defaults',
      () async {
    final prefs = await hydratePrefsWithTimeout(
      loadPrefs: () => Completer<SharedPreferences>().future,
      timeout: const Duration(milliseconds: 10),
    );
    await prefs.setBool('onboarded_v1', true);
    expect(prefs.getBool('onboarded_v1'), isTrue);
  });

  test('fallback sets BFU sentinel so post-Sentry-init can emit a breadcrumb',
      () async {
    final prefs = await hydratePrefsWithTimeout(
      loadPrefs: () => Completer<SharedPreferences>().future,
      timeout: const Duration(milliseconds: 10),
    );
    expect(prefs.getBool(kPrefsBfuFallbackSentinelKey), isTrue);
  });

  test('happy path does NOT set the BFU sentinel', () async {
    final prefs = await hydratePrefsWithTimeout(
      loadPrefs: SharedPreferences.getInstance,
      timeout: const Duration(seconds: 5),
    );
    expect(prefs.getBool(kPrefsBfuFallbackSentinelKey), isNull);
  });

  test('late-resolving original loader does not pollute fallback prefs',
      () async {
    // Branch 3 of the file-header contract: when the original
    // `getInstance()` future eventually completes (e.g., Android storage
    // unlocks 30s post-boot), its resolution must not leak back into
    // the fallback instance the boot path is already using. The
    // `setMockInitialValues({})` call resets the package-level
    // `_completer` (verified against shared_preferences 2.5.5
    // source), so the late completion lands on a now-detached completer.
    final original = Completer<SharedPreferences>();
    final fallback = await hydratePrefsWithTimeout(
      loadPrefs: () => original.future,
      timeout: const Duration(milliseconds: 10),
    );
    // Only the internal BFU sentinel is set by the fallback path; no
    // user-facing keys should be present.
    expect(
      fallback.getKeys().where((k) => k != kPrefsBfuFallbackSentinelKey),
      isEmpty,
    );

    // Now resolve the original loader as if Android storage came back
    // online. The fallback instance must remain unaffected.
    SharedPreferences.setMockInitialValues({'kc_theme_id': 'graphite-late'});
    final lateResolved = await SharedPreferences.getInstance();
    original.complete(lateResolved);
    // Drain microtasks so any spurious completion would surface.
    await Future<void>.delayed(Duration.zero);

    // Fallback handle still points at the empty in-memory store from
    // the timeout branch; the late resolution did not retroactively
    // populate it.
    expect(fallback.getString('kc_theme_id'), isNull);
    expect(
      fallback.getKeys().where((k) => k != kPrefsBfuFallbackSentinelKey),
      isEmpty,
    );
  });

  test('falls back when the loader throws a non-timeout Exception',
      () async {
    // PlatformException, MissingPluginException, or any other Exception
    // out of the platform channel must route to the same fallback —
    // boot is guaranteed a usable prefs handle regardless of failure
    // mode. The fallback path is identical to the timeout branch so
    // the post-Sentry-init breadcrumb fires uniformly.
    final fallback = await hydratePrefsWithTimeout(
      loadPrefs: () => Future<SharedPreferences>.error(
        Exception('platform channel surface error'),
      ),
      timeout: const Duration(seconds: 1),
    );
    expect(fallback.getBool(kPrefsBfuFallbackSentinelKey), isTrue);
    expect(
      fallback.getKeys().where((k) => k != kPrefsBfuFallbackSentinelKey),
      isEmpty,
    );
  });

  test('does NOT catch Error subtypes (programmer-error contract)',
      () async {
    // StateError/AssertionError/etc. are Error, not Exception. The
    // helper deliberately lets them propagate so genuine contract
    // violations aren't papered over by the fallback path.
    await expectLater(
      hydratePrefsWithTimeout(
        loadPrefs: () => Future<SharedPreferences>.error(
          StateError('helper contract violated'),
        ),
        timeout: const Duration(seconds: 1),
      ),
      throwsA(isA<StateError>()),
    );
  });

  test('default timeout constant is the documented 10 seconds', () {
    expect(kDefaultPrefsHydrationTimeout, const Duration(seconds: 10));
  });
}
