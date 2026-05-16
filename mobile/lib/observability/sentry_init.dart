// Lazy Sentry bootstrap.
//
// Called from `main.dart` BEFORE `runApp` so the SDK can install its
// FlutterError + PlatformDispatcher handlers around the entire app
// lifetime. The off path is a single `shared_preferences` read — no
// Sentry SDK calls at all when the user is opted out.
//
// DSN sourcing:
//   - `--dart-define=SENTRY_DSN=<dsn>` at build time, captured by
//     `const String.fromEnvironment`. When unset (local dev, CI builds
//     without the secret wired), the const resolves to `''` and we skip
//     init entirely. The opt-in toggle still flips the preference, but
//     no events leave the device — documented in OBSERVABILITY.md so
//     operators aren't surprised.
//   - DSN extraction from the binary is intrinsic to `--dart-define`
//     (any `strings`/`apktool` run recovers it). Mitigations live in the
//     Sentry project config: release-tagged inbound filter, per-project
//     event rate cap. See OBSERVABILITY.md.
//
// PII handling: every event passes through [scrubEvent] before transport.
// We additionally disable performance + profiling SDKs (their breadcrumbs
// can carry HTTP request bodies that the scrubber would need to deeply
// audit).

import 'package:flutter/foundation.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:sentry_flutter/sentry_flutter.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'pii_scrubber.dart';

/// SharedPreferences key used by the Settings toggle and this init path
/// to track the user's opt-in state.
const String kSentryOptInPrefsKey = 'sentry_opt_in';

/// Compile-time DSN. Defaults to empty when the build did not supply
/// `--dart-define=SENTRY_DSN=<dsn>`.
const String kSentryDsn = String.fromEnvironment('SENTRY_DSN');

/// Initializes Sentry if and only if:
///   1. the `sentry_opt_in` shared_preferences flag is `true`, and
///   2. [kSentryDsn] is non-empty (built with a DSN).
///
/// Returns `true` if Sentry was actually initialized, `false` if either
/// gate was closed. Callers (`main.dart`) ignore the return value; tests
/// inspect it.
Future<bool> initSentryIfOptedIn() async {
  final prefs = await SharedPreferences.getInstance();
  final optedIn = prefs.getBool(kSentryOptInPrefsKey) ?? false;
  if (!optedIn) return false;
  if (kSentryDsn.isEmpty) return false;

  await SentryFlutter.init((options) {
    options.dsn = kSentryDsn;
    // Defence-in-depth on top of [scrubEvent]: Sentry's own PII filter
    // strips IPs + Cookie headers when this is false.
    options.sendDefaultPii = false;
    // We capture crashes, not performance — disable tracing so
    // performance-span breadcrumbs don't bypass [scrubEvent]'s targeted
    // scrub. Profiling is off by default at 8.x and is left unset (the
    // SDK marks `profilesSampleRate` experimental).
    options.tracesSampleRate = 0.0;
    options.attachStacktrace = true;
    options.beforeSend = scrubEvent;
    options.environment = kReleaseMode ? 'production' : 'development';
  });

  // Tag the release so Sentry's project-level inbound filter can drop
  // events whose `release` doesn't match the published binary's version.
  // Awaited after init so init isn't blocked on the platform channel.
  try {
    final info = await PackageInfo.fromPlatform();
    Sentry.configureScope((scope) {
      scope.setTag('release', 'kubecenter-mobile@${info.version}');
      scope.setTag('build', info.buildNumber);
    });
  } catch (_) {
    // PackageInfo can throw on platforms without the plugin registered
    // (e.g., some test contexts). Init is already done — swallow.
  }

  return true;
}

/// Tears down a previously-initialized Sentry SDK. Used by the Settings
/// toggle when the user flips opt-in to false at runtime.
///
/// Awaiting `Sentry.close()` flushes any in-flight events before the
/// transport shuts down so we don't drop a crash currently mid-send.
Future<void> closeSentry() async {
  await Sentry.close();
}
