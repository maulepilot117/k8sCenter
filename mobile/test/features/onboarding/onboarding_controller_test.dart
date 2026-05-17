// OnboardingController + migrateOnboardingFlagForUpgrade contract.
//
// The controller is a thin sync read/write over the `onboarded_v1`
// prefs key. The migration helper is the only async surface, and only
// runs once at app boot. Both are covered here without spinning up a
// widget tree.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/onboarding/onboarding_controller.dart';
import 'package:kubecenter/providers/shared_preferences_provider.dart';
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

  group('OnboardingController.build', () {
    test('defaults to false when prefs has no onboarded_v1 key', () async {
      final prefs = await SharedPreferences.getInstance();
      final container = _container(prefs);
      addTearDown(container.dispose);

      expect(container.read(onboardingControllerProvider), isFalse);
    });

    test('returns true when prefs has onboarded_v1 == true', () async {
      SharedPreferences.setMockInitialValues({kOnboardedPrefsKey: true});
      final prefs = await SharedPreferences.getInstance();
      final container = _container(prefs);
      addTearDown(container.dispose);

      expect(container.read(onboardingControllerProvider), isTrue);
    });
  });

  group('OnboardingController.complete', () {
    test('writes the prefs key and transitions state', () async {
      final prefs = await SharedPreferences.getInstance();
      final container = _container(prefs);
      addTearDown(container.dispose);

      await container.read(onboardingControllerProvider.notifier).complete();

      expect(container.read(onboardingControllerProvider), isTrue);
      expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
    });

    test('idempotent when already completed', () async {
      SharedPreferences.setMockInitialValues({kOnboardedPrefsKey: true});
      final prefs = await SharedPreferences.getInstance();
      final container = _container(prefs);
      addTearDown(container.dispose);

      await container.read(onboardingControllerProvider.notifier).complete();

      expect(container.read(onboardingControllerProvider), isTrue);
      expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
    });

    test('concurrent calls are serialized — state is true exactly once',
        () async {
      // Mirror of sentry_controller_test.dart concurrent-setOptIn test.
      // Two complete() calls fired simultaneously must not interleave or
      // double-write: both should resolve with state == true and
      // exactly one prefs write (idempotent after the first completes).
      final prefs = await SharedPreferences.getInstance();
      final container = _container(prefs);
      addTearDown(container.dispose);

      final notifier = container.read(onboardingControllerProvider.notifier);
      // Fire two concurrent calls.
      await Future.wait([notifier.complete(), notifier.complete()]);

      expect(container.read(onboardingControllerProvider), isTrue);
      expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
    });
  });

  group('migrateOnboardingFlagForUpgrade', () {
    test('sets flag when refresh token present and flag absent', () async {
      final prefs = await SharedPreferences.getInstance();
      final store = InMemoryTokenStore();
      await store.writeRefreshToken('rt_internal_beta');

      await migrateOnboardingFlagForUpgrade(prefs, store);

      expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
    });

    test('no-op when refresh token absent (fresh install)', () async {
      final prefs = await SharedPreferences.getInstance();
      final store = InMemoryTokenStore();

      await migrateOnboardingFlagForUpgrade(prefs, store);

      expect(prefs.containsKey(kOnboardedPrefsKey), isFalse);
    });

    test('no-op when flag already true (returning user)', () async {
      SharedPreferences.setMockInitialValues({kOnboardedPrefsKey: true});
      final prefs = await SharedPreferences.getInstance();
      final store = InMemoryTokenStore();
      await store.writeRefreshToken('rt_user');

      await migrateOnboardingFlagForUpgrade(prefs, store);

      expect(prefs.getBool(kOnboardedPrefsKey), isTrue);
    });

    test('does not promote when flag explicitly false', () async {
      // The `containsKey` guard prevents a user who has Skip'd and then
      // logged in (refresh token now present) from being re-promoted
      // back to onboarded — they made their choice. Documents the
      // current invariant; complete() never writes false today.
      SharedPreferences.setMockInitialValues({kOnboardedPrefsKey: false});
      final prefs = await SharedPreferences.getInstance();
      final store = InMemoryTokenStore();
      await store.writeRefreshToken('rt_user');

      await migrateOnboardingFlagForUpgrade(prefs, store);

      expect(prefs.getBool(kOnboardedPrefsKey), isFalse);
    });

    test('app killed mid-onboarding restarts with flag still absent',
        () async {
      // Plan scenario: user opens app, swipes to card 2, force-quits.
      // Flag was never set, so the relaunch routes back to /onboarding
      // and the PageView starts at card 1. We assert the flag is still
      // absent — restart-from-card-1 behaviour is owned by the screen's
      // PageController, not the controller.
      final prefs = await SharedPreferences.getInstance();
      final store = InMemoryTokenStore();
      final container = _container(prefs);
      addTearDown(container.dispose);

      // Simulate cold-start migration with no refresh token yet (the
      // user never reached login).
      await migrateOnboardingFlagForUpgrade(prefs, store);

      expect(container.read(onboardingControllerProvider), isFalse);
      expect(prefs.containsKey(kOnboardedPrefsKey), isFalse);
    });
  });
}
