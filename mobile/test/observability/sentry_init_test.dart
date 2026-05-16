// Verifies the two unit-testable gates in initSentryIfOptedIn:
//   1. opted-out → returns false, no SDK work.
//   2. opted-in + empty DSN → returns false, no SDK work.
//
// We can't exercise the success path here because:
//   - kSentryDsn is a compile-time const, fixed to '' in test builds
//     (no --dart-define=SENTRY_DSN flag).
//   - SentryFlutter.init is a static method, not injectable, so we can't
//     stub it. Running it for real would attempt a network handshake.
//
// PR-5j is responsible for a manual end-to-end verification on a real
// device before submitting to stores.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/observability/sentry_init.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('returns false when opt-in pref is absent', () async {
    final prefs = await SharedPreferences.getInstance();
    final initialized = await initSentryIfOptedIn(prefs);
    expect(initialized, isFalse);
  });

  test('returns false when opt-in pref is explicitly false', () async {
    SharedPreferences.setMockInitialValues({kSentryOptInPrefsKey: false});
    final prefs = await SharedPreferences.getInstance();
    final initialized = await initSentryIfOptedIn(prefs);
    expect(initialized, isFalse);
  });

  test('returns false when opt-in is true but DSN is empty', () async {
    // kSentryDsn resolves to '' in tests (no --dart-define), so the
    // second gate closes regardless of the prefs flag.
    SharedPreferences.setMockInitialValues({kSentryOptInPrefsKey: true});
    final prefs = await SharedPreferences.getInstance();
    final initialized = await initSentryIfOptedIn(prefs);
    expect(initialized, isFalse);
    expect(kSentryDsn, isEmpty);
  });
}
