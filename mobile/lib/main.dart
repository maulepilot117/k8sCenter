// Entry point. Hydrates SharedPreferences before runApp so the theme
// controller reads synchronously, initializes Sentry only when the user
// has opted in (default off), then triggers the auth bootstrap so the
// redirect guard sees Authenticated/Unauthenticated by the time the
// first frame paints.

import 'dart:developer' as developer;

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
  //
  // The try/catch ensures that if backendUrlProvider throws a StateError
  // (release build without --dart-define=BACKEND_URL), the app transitions
  // off the splash screen instead of freezing on AuthInitializing forever.
  // (Finding P1-4)
  unawaited(() async {
    try {
      await container.read(authRepositoryProvider.notifier).bootstrap();
      await container.read(universalLinkListenerProvider).start();
    } on StateError catch (e, st) {
      // backendUrlProvider rejected — release build is misconfigured.
      // Force the router off the splash screen so the user sees the login
      // screen (with an error message) rather than being frozen on the
      // splash indefinitely.
      FlutterError.reportError(FlutterErrorDetails(
        exception: e,
        stack: st,
        library: 'auth bootstrap',
        context: ErrorDescription(
          'backendUrlProvider validation failed; transitioning to '
          'Unauthenticated so the login screen renders (Finding P1-4)',
        ),
      ));
      container
          .read(authRepositoryProvider.notifier)
          .failBootstrap(e.message);
    } catch (e, st) {
      developer.log(
        'bootstrap threw unexpectedly: $e',
        name: 'main',
        error: e,
        stackTrace: st,
      );
      FlutterError.reportError(FlutterErrorDetails(
        exception: e,
        stack: st,
        library: 'auth bootstrap',
      ));
      container
          .read(authRepositoryProvider.notifier)
          .failBootstrap('bootstrap failed: $e');
    }
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
