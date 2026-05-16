# Mobile observability — Sentry crash reporting

Status: PR-5a foundation. Settings toggle and PII-scrubbing transport
landed; per-environment DSN is provisioned at PR-5j time. Default state
is **opt-out**.

## Opt-in posture

Sentry is **off by default**. A user enables crash reporting via
**Settings → Crash reporting → Send crash reports**. The toggle persists
to `shared_preferences` under the key `sentry_opt_in`.

- **Off path** (default): one `shared_preferences` read at startup. No
  Sentry SDK code executes. `Sentry.*` static calls anywhere in the app
  are no-ops.
- **On path**: `lib/observability/sentry_init.dart#initSentryIfOptedIn`
  calls `SentryFlutter.init(...)` before `runApp`. The session-wide
  `beforeSend` hook is the PII scrubber in
  `lib/observability/pii_scrubber.dart`.

Toggling the switch at runtime triggers init (off → on) or
`Sentry.close()` (on → off) so a change doesn't require an app restart.

## DSN handling

The DSN is sourced at build time:

```
flutter build apk --dart-define=SENTRY_DSN=https://<key>@<org>.ingest.sentry.io/<project>
```

When the build does **not** pass `--dart-define=SENTRY_DSN`, the const
`kSentryDsn` resolves to the empty string and the init path becomes a
no-op even with opt-in enabled. The opt-in toggle still flips the
preference so a subsequent build with the DSN wired will start sending
events immediately.

**DSN extractability**: `--dart-define` values are embedded in the
compiled binary as plain string literals. `strings` on the APK or
`apktool` on the AAB will recover the DSN. This is an inherent property
of `--dart-define`, not a leak — every Flutter app with a Sentry build
ships its DSN this way.

Mitigations live in the Sentry project, not on the device:

1. **Inbound-data filter** keyed on `release` tag — drop events whose
   release id doesn't match the published binary's version.
2. **Per-project rate cap** to bound quota exhaustion if an attacker
   spams the DSN.
3. **Release-tagged events** — `kubecenter-mobile@<version>+<build>` so
   legitimate events are unambiguous.

Configured at PR-5j time alongside the actual DSN provisioning.

## PII scrubbing policy

Every event passes through `scrubEvent` (in
`lib/observability/pii_scrubber.dart`) before transport. The scrub is
**layered**, deliberately — collapsing it into a single regex hits both
false-positive and false-negative cliffs.

| Layer | Source | Action |
|------|--------|--------|
| 1 | `event.user` (username, email, IP) | **Drop unconditionally.** Sentry's `sendDefaultPii: false` covers most of this, but we wipe the field regardless to defend against accidental SDK upgrades that re-enable it. |
| 2 | Request bodies, breadcrumb query strings, cookies | **Drop wholesale.** The k8sCenter API echoes resource names, namespaces, and YAML back to the caller — none of which belongs in a shared crash project. |
| 3 | Exception messages, breadcrumb messages, top-level message | **Positional k8s-path scrub.** Only the segments AFTER known keys (`/v1/<bucket>/<kind>/<ns>/<name>`, `namespace=`, `name=`) are replaced. NOT a generic-name regex. |
| 4 | FCM device tokens | **Pattern-strip.** `[A-Za-z0-9_:-]{100,}` is effectively unique to FCM at that length. |
| 5 | Stack frames (`abs_path`, `filename`, `module`, `function`) | **Preserved.** Source paths describe build artifacts, not runtime state. Scrubbing them destroys crash debuggability for zero privacy gain. |
| 6 | HTTP request headers | **Authorization / Cookie / Set-Cookie / X-CSRF-* / X-Requested-With dropped.** Other headers (Content-Type, User-Agent) preserved. |

A naive `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` matcher over 12-char tokens
has too high a false-positive rate on Dart symbols and URL paths AND
fails to catch sub-12-char resource names like `vault-token`. The
positional approach trades regex simplicity for accuracy.

## Adding new instrumentation safely

When adding new code that logs to Sentry (direct `Sentry.captureMessage`
calls, or Dio request enrichment), audit the payload against the layers
above. The default assumption is that any free-text string you pass into
Sentry **will** reach the shared project — write your code as if the
scrubber didn't exist, then verify it does the right thing.

If your new payload shape doesn't match an existing scrub layer (e.g.,
you start logging Kubernetes secret values directly — DON'T — but if you
did), extend `pii_scrubber.dart` with a new layer and add a regression
test. The scrubber's test file lives at
`test/observability/pii_scrubber_test.dart`.

## Debug-build guard

`scrubEvent` returns `null` (drop event) when `!kReleaseMode` — i.e., in
both debug and profile builds. This is what keeps developer
simulator/emulator runs AND performance-harness profile builds out of
the shared project, since release/debug/profile builds all share the
same DSN provisioned at build time. Only production binaries
(TestFlight, App Store, Play production), which build with `--release`,
pass the gate.

## App-launch safety

`initSentryIfOptedIn` is wrapped in try/catch at TWO layers (the SDK
init itself in `sentry_init.dart`, and the call site in `main.dart`).
Even if the Sentry SDK throws during init — DSN parse error, native
channel panic, transport refusing the handshake — the app continues to
launch. The init function returns `false` and `runApp` proceeds. This is
verified by the unit tests in `test/observability/sentry_init_test.dart`
and the documented "app does not fail to launch even when init throws"
contract; if you change init behaviour, do not weaken this gate.

## Deferred from PR-5a

- **FCM device-token revoke tile in Settings → About.** The plan calls
  for a row showing the registered device with a "Revoke" action that
  calls `DELETE /v1/notifications/devices/{id}`. The backend supports
  it (`GET /v1/notifications/devices` + `DELETE /v1/notifications/devices/{id}`)
  but the mobile-side repository plumbing (list devices, find the
  current device by token, delete by id, re-register) is non-trivial
  and isn't a foundation any other M5 PR depends on. Tracked as a
  follow-up; can ship in a v1.1 patch or as part of PR-5e's write-parity
  work.
- **Sentry release tag verification at the project level.** PR-5j wires
  the inbound filter against the published version pattern.
- **Synthetic test event before public release.** PR-5j fires a real
  crash on a real device and audits the Sentry event payload for
  unexpected PII before submitting to stores.
- **Production-track DSN gating.** PR-5j Fastlane lanes (`promote_ios`,
  `promote_android`) must NOT pass `--dart-define=SENTRY_DSN` until the
  Sentry project-side inbound filter + release tag filter + rate cap
  are configured. Until then, the DSN should only be wired into the
  internal-beta TestFlight / Play Internal lanes so a misconfigured
  filter can't burn project quota from production builds.
