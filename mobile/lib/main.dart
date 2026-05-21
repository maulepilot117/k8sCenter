// Entry point. Hydrates SharedPreferences before runApp so the theme
// controller reads synchronously, initializes Sentry only when the user
// has opted in (default off), then triggers the auth bootstrap so the
// redirect guard sees Authenticated/Unauthenticated by the time the
// first frame paints.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'app.dart';
import 'auth/auth_repository.dart';
import 'auth/auth_state.dart';
import 'auth/secure_storage.dart';
import 'auth/universal_link_listener.dart';
import 'features/onboarding/onboarding_controller.dart';
import 'notifications/fcm_registration.dart';
import 'observability/sentry_init.dart';
import 'providers/prefs_bootstrap.dart';
import 'providers/shared_preferences_provider.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  // Bounded hydration so a Before-First-Unlock Android storage hang
  // can't keep the app on a blank screen. See [hydratePrefsWithTimeout]
  // for the fallback semantics. #270.
  final prefs = await hydratePrefsWithTimeout();

  // Lazy Sentry bootstrap. Returns false (no work) when the user has
  // not opted in, when the build doesn't carry --dart-define=SENTRY_DSN,
  // or on any init error. The opt-out path is a single prefs read.
  // Wrapped a second time at the call site so even a wildly unanticipated
  // throw (e.g., the Dart isolate's zone is in a weird state) cannot
  // prevent runApp from being reached.
  try {
    await initSentryIfOptedIn(prefs);
  } catch (error) {
    debugPrint('initSentryIfOptedIn threw: $error');
  }

  final container = ProviderContainer(
    overrides: [
      sharedPreferencesProvider.overrideWithValue(prefs),
    ],
  );

  // M5 PR-5g: internal-beta upgrade detection. Runs before the first
  // router redirect so the synchronous `onboarded_v1` check sees a
  // stable value. Errors are non-fatal — worst case is showing the
  // tour once to an upgrade user.
  try {
    await migrateOnboardingFlagForUpgrade(
      prefs,
      container.read(secureTokenStoreProvider),
    );
  } catch (error) {
    debugPrint('migrateOnboardingFlagForUpgrade threw: $error');
  }

  // Register for FCM the first time auth lands on Authenticated.
  // Conditional Firebase init means this is a no-op when the operator
  // hasn't dropped in google-services.json / GoogleService-Info.plist.
  container.listen<AuthState>(authRepositoryProvider, (prev, next) {
    if (next is AuthAuthenticated && prev is! AuthAuthenticated) {
      unawaited(container.read(fcmRegistrationProvider).ensureRegistered());
    }
  }, fireImmediately: true);

  // Bootstrap first; the universal-link listener only starts after auth
  // state has settled, so a queued OIDC callback can't race bootstrap
  // and clobber the identity. Both run in the background — the router
  // renders the splash (AuthInitializing) until bootstrap resolves; the
  // listener is a no-op when the build was not produced with
  // --dart-define=UNIVERSAL_LINK_HOST. Drains the initial link (cold
  // start: redirect arrived while app was terminated) on the same call.
  unawaited(() async {
    await container.read(authRepositoryProvider.notifier).bootstrap();
    await container.read(universalLinkListenerProvider).start();
  }());

  runApp(
    UncontrolledProviderScope(
      container: container,
      child: const KubeCenterApp(),
    ),
  );
}

void unawaited(Future<void> future) {
  // Intentionally not awaited; mirrors `package:async`'s helper without
  // pulling the dep in just for one call site.
}
