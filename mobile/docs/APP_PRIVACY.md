# App Privacy

This document records the answers that go into Apple's App Privacy questionnaire and Google Play's Data Safety form. Operators submitting to the public stores should fill the actual questionnaires with these answers verbatim. Both stores require the privacy policy URL to be live before review; the canonical URL is <https://kubecenter.io/privacy>.

## What this app is

k8sCenter Mobile is a **thin client** for the user's self-hosted k8sCenter Kubernetes management server. It is not a standalone Kubernetes client. It does not connect directly to Kubernetes API servers. It does not store Kubernetes cluster credentials on the device — those stay encrypted with AES-256-GCM on the user's k8sCenter backend. The mobile app stores only its own session JWT after sign-in and a Firebase Cloud Messaging device token for push notifications.

Mirror this paragraph in the App Store Review Information → Notes field. Reviewers consistently ask about Kubernetes-credentials handling on apps in this category; pre-empting the question avoids guideline 5.1.2 (Data Collection and Storage) holds.

## Data Types collected

| Apple category | Type | Linked to user? | Used for tracking? | Purpose |
|---|---|---|---|---|
| Identifiers | Device ID (FCM token) | Linked (after sign-in) | No | App Functionality (push notifications) |
| Diagnostics | Crash Data (Sentry) | Not linked | No | App Functionality |
| Diagnostics | Performance Data (Sentry) | Not linked | No | App Functionality |
| Usage Data | Product Interaction (Sentry breadcrumbs) | Not linked | No | App Functionality |

Sentry rows are **opt-in only**. Default state is OFF — no data leaves the device unless the user toggles the Sentry switch in Settings. On-device PII scrub runs before any event is queued for upload.

## Data Types NOT collected

Everything outside the table above. Explicitly:

- No contact info (email, name, phone, address)
- No health & fitness data
- No financial info
- No location data (no GPS, no IP-based geolocation)
- No sensitive info (race, sexual orientation, religion, etc.)
- No contacts (phone book)
- No user content (photos, videos, audio, messages, files)
- No browsing or search history
- No purchases
- No advertising identifiers (IDFA not requested)
- No other user-content data

## Play Data Safety mapping

Play's form maps roughly to Apple's. Equivalent answers:

| Play category | Answer |
|---|---|
| Personal info | Not collected |
| Financial info | Not collected |
| Health and fitness | Not collected |
| Messages | Not collected |
| Photos and videos | Not collected |
| Audio | Not collected |
| Files and docs | Not collected |
| Calendar | Not collected |
| Contacts | Not collected |
| App activity | Sentry breadcrumbs (opt-in, not linked, not shared, encrypted in transit, deletable via Sentry support) |
| Web browsing | Not collected |
| App info and performance | Sentry crash + performance data (opt-in, not linked, not shared, encrypted in transit, deletable via Sentry support) |
| Device or other IDs | FCM device token (linked after sign-in, not shared with third parties beyond Google Firebase, encrypted in transit, deletable on account sign-out) |

## Third-party services

| Service | Purpose | Data sent | Opt-in? |
|---|---|---|---|
| Firebase Cloud Messaging (Google) | Push notifications | Device token, message payload from the user's k8sCenter server | No — required for push notifications. Disabling FCM on the user's k8sCenter server disables this on every connected mobile device. |
| Sentry | Crash + performance reporting | Stack traces (obfuscated by `--obfuscate --split-debug-info`), breadcrumb URLs (query strings scrubbed), exception messages (k8s path segments and `namespace=`/`name=` key-values scrubbed), FCM tokens stripped via regex | Yes — default OFF. Toggled in Settings → Crash Reporting. |

No advertising SDKs. No analytics SDKs. No social-login SDKs (OIDC sign-in goes directly to the operator's chosen IdP via the system browser, not via any SDK).

## On-device PII scrub specifics

When Sentry is enabled, the `beforeSend` callback runs the following before any event leaves the device:

- **`user.*`** fields stripped unconditionally.
- **Request bodies** stripped wholesale on breadcrumbs and HTTP integrations — too much risk of secret leakage to scrub piecemeal.
- **Breadcrumb URL query strings** stripped wholesale (same reason).
- **Exception and breadcrumb messages** run through a positional scrubber that matches against `/v1/...` k8s path segments and `namespace=...`/`name=...` key-value pairs; values get replaced with `[REDACTED]` while structure is preserved for debuggability.
- **FCM tokens** stripped via `[A-Za-z0-9_-]{100,}` regex.
- **Stack frame fields** (`abs_path`, `filename`, `module`, `function`) preserved unscrubbed — needed for the reports to be useful.
- **Debug-mode events** (`kReleaseMode == false`) dropped at source so developer-session frames never reach the shared Sentry project.

Refer to `mobile/lib/observability/pii_scrubber.dart` for the live implementation. Refer to `mobile/docs/OBSERVABILITY.md` for the rationale on each scrub category.

## User rights

- **Right to opt out of crash reporting**: Settings → Crash Reporting → toggle off. Effective immediately. Pending events in the on-device queue are dropped.
- **Right to delete account data**: Sign out + uninstall the app. The session JWT lives only in iOS Keychain / Android Keystore; uninstall removes it. The FCM device token is invalidated on sign-out via `POST /v1/notifications/devices/<id>/unregister` on the k8sCenter server.
- **Right to access**: All data this app holds is local to the device or held by the user's own k8sCenter server. Contact the k8sCenter operator (the user's own organization) for access to server-side audit logs.
- **Right to deletion**: Request via the support email on the App Store listing. The k8sCenter project itself doesn't operate a multi-tenant backend, so the only data we can delete is what the user's own k8sCenter server stores. Reach out and we'll point at the right operator.

## Contact

Privacy concerns: <https://github.com/maulepilot117/k8sCenter/issues> (open a confidential issue if needed). For App Store / Play Store reviewer questions, the Review Information notes field repeats the "What this app is" section above.

## Submission checklist

Before submitting either App Store or Play Store reviews, confirm:

- [ ] <https://kubecenter.io/privacy> is reachable and returns 200 with `text/html`.
- [ ] App Store Connect → App Privacy → all categories filled per the table above.
- [ ] Play Console → App Content → Data Safety form filled per the Play mapping table above.
- [ ] App Store Review Notes field contains the "What this app is" paragraph verbatim.
- [ ] Sentry test event sent from a release build via a synthetic crash with realistic k8s resource names; the payload inspected in Sentry to confirm the scrubber is doing its job. If anything unexpected leaks, fix the scrubber before submitting.
