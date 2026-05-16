// Verifies the Sentry opt-in controller: defaults to off when no
// preference is stored, persists toggles through SharedPreferences, and
// is idempotent on no-op transitions.
//
// `SentryFlutter.init` and `Sentry.close` are non-injectable static
// surfaces, so we can't directly assert on the SDK side. We test what
// IS observable: the prefs key and the controller state. The init/close
// calls themselves are exercised indirectly — they're no-ops in tests
// because `kSentryDsn` is empty (no `--dart-define=SENTRY_DSN`).

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/features/settings/sentry_controller.dart';
import 'package:kubecenter/observability/sentry_init.dart';
import 'package:kubecenter/theme/theme_controller.dart';
import 'package:shared_preferences/shared_preferences.dart';

ProviderContainer _container(SharedPreferences prefs) {
  return ProviderContainer(
    overrides: [sharedPreferencesProvider.overrideWithValue(prefs)],
  );
}

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('defaults to false when prefs has no sentry_opt_in key', () async {
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    expect(container.read(sentryControllerProvider), isFalse);
  });

  test('build() returns true when prefs has sentry_opt_in == true',
      () async {
    SharedPreferences.setMockInitialValues({kSentryOptInPrefsKey: true});
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    expect(container.read(sentryControllerProvider), isTrue);
  });

  test('setOptIn(true) writes the prefs key and transitions state',
      () async {
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    await container.read(sentryControllerProvider.notifier).setOptIn(true);

    expect(container.read(sentryControllerProvider), isTrue);
    expect(prefs.getBool(kSentryOptInPrefsKey), isTrue);
  });

  test('setOptIn(false) transitions to false', () async {
    SharedPreferences.setMockInitialValues({kSentryOptInPrefsKey: true});
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    await container.read(sentryControllerProvider.notifier).setOptIn(false);

    expect(container.read(sentryControllerProvider), isFalse);
    expect(prefs.getBool(kSentryOptInPrefsKey), isFalse);
  });

  test('setOptIn matching current state is a no-op (no prefs write)',
      () async {
    // Start with opt-in already false (the default). Toggling to false
    // again should NOT touch the prefs key.
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    await container.read(sentryControllerProvider.notifier).setOptIn(false);

    // The key is null (never written) — confirmed-idempotent.
    expect(prefs.getBool(kSentryOptInPrefsKey), isNull);
    expect(container.read(sentryControllerProvider), isFalse);
  });

  test('concurrent setOptIn calls are serialized', () async {
    // Two rapid setOptIn calls fired without intermediate awaits. The
    // controller's _inflight serializer should drain the first before
    // evaluating the second's no-op fast path. End state must reflect
    // the LAST call's intent.
    final prefs = await SharedPreferences.getInstance();
    final container = _container(prefs);
    addTearDown(container.dispose);

    final notifier = container.read(sentryControllerProvider.notifier);
    final f1 = notifier.setOptIn(true);
    final f2 = notifier.setOptIn(false);
    await Future.wait([f1, f2]);

    expect(container.read(sentryControllerProvider), isFalse);
    expect(prefs.getBool(kSentryOptInPrefsKey), isFalse);
  });
}
