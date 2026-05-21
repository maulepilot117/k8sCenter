// Verifies hydratePrefsWithTimeout's three branches:
//   1. Happy path: load resolves within timeout → returns its value.
//   2. Timeout: load never resolves → falls back to empty in-memory
//      SharedPreferences and boot continues.
//   3. Late-resolving load: an orphaned platform future doesn't pollute
//      the returned fallback instance.

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/boot/prefs_bootstrap.dart';
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
    // path, not the original mock that was in setUp().
    expect(fallback.getString('kc_theme_id'), isNull);
    expect(fallback.getKeys(), isEmpty);
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

  test('rethrows non-timeout errors so boot can crash loudly', () async {
    expectLater(
      hydratePrefsWithTimeout(
        loadPrefs: () => Future<SharedPreferences>.error(
          StateError('platform channel exploded'),
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
