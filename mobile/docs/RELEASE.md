# Mobile release setup

The mobile app's release pipeline is **opt-in**. Operators who don't ship to TestFlight or the Play Internal track can ignore this entirely — the app builds and runs in homelab without any of these prerequisites. CI's `deploy_ios` and `deploy_android` jobs auto-skip when their secrets are absent.

This doc walks an operator through bringing the pipeline up the first time. None of these steps are reversible, none take more than a few hours of wall-clock time, but several have multi-day approval windows (Apple Developer Program enrollment, in particular).

## Prerequisite tracker

| Track | Required to | Lead time |
|---|---|---|
| Apple Developer Program | Upload to TestFlight | 24–48h approval |
| Google Play Console | Upload to Play Internal | Same-day |
| FCM project | Receive push notifications (PR-1f) | Same-day |
| Universal Link domain | HTTPS deep links (vs. `k8scenter://` only) | Depends on existing infra |

## iOS setup

### 1. Apple Developer Program enrollment

Enroll the team at <https://developer.apple.com/programs/>. $99/yr. Have a US D-U-N-S number ready if enrolling as a company; individuals enroll under personal Apple ID.

### 2. Create the App Store Connect record

In App Store Connect → My Apps → +. Bundle ID `io.kubecenter.kubecenter`. SKU is operator's choice.

### 3. Create an App Store Connect API key

App Store Connect → Users and Access → Integrations → App Store Connect API → Generate API Key. Role: App Manager. Download the `.p8` file (one-time download — keep it safe). Note the Key ID and Issuer ID.

The CI workflow expects the API key as a JSON blob in the `APPSTORE_CONNECT_API_KEY` secret with this shape:

```json
{
  "key_id": "ABC123DEF4",
  "issuer_id": "12345678-aaaa-bbbb-cccc-1234567890ab",
  "key": "-----BEGIN PRIVATE KEY-----\nMIG...==\n-----END PRIVATE KEY-----\n",
  "in_house": false
}
```

### 4. Set up `fastlane match` for cert + provisioning profile storage

`match` keeps your iOS signing certs and provisioning profiles in a separate, encrypted git repo so CI can fetch them without manual steps.

1. Create a private GitHub repo (e.g., `your-org/k8scenter-mobile-certs`). Empty.
2. From a developer Mac with the team's Apple ID logged in:
   ```bash
   cd mobile
   bundle init && bundle add fastlane && bundle install
   bundle exec fastlane match init    # Choose `git`, paste your repo URL.
   bundle exec fastlane match appstore --git_url <repo-url>
   ```
   `match` will prompt for a passphrase — choose carefully, this is the `MATCH_PASSWORD` secret.
3. Verify the certs landed in the repo (encrypted) before continuing.

### 5. Set the GitHub Actions secrets

In Settings → Secrets and variables → Actions:

**iOS secrets:**

| Secret | Value |
|---|---|
| `MATCH_GIT_URL` | `https://github.com/your-org/k8scenter-mobile-certs.git` |
| `MATCH_PASSWORD` | The passphrase set during `match init` |
| `APPSTORE_CONNECT_API_KEY` | The JSON blob from step 3 |
| `APPLE_ID` | Apple ID email used for the dev account |
| `APPLE_TEAM_ID` | 10-char team ID from Apple Developer → Membership |

**Android upload-signing secrets (required for release AABs — finding P1-5):**

| Secret | Value |
|---|---|
| `ANDROID_UPLOAD_KEYSTORE_BASE64` | `base64 -w0 upload-keystore.jks` output from step 2 above |
| `ANDROID_UPLOAD_KEY_ALIAS` | `upload` (the alias passed to keytool above) |
| `ANDROID_UPLOAD_KEY_PASSWORD` | The key password set during `keytool -genkey` |
| `ANDROID_UPLOAD_STORE_PASSWORD` | The keystore password set during `keytool -genkey` |

**Shared secrets (required for both iOS and Android release builds — finding P1-4):**

| Secret | Value |
|---|---|
| `BACKEND_URL` | Operator's production HTTPS backend URL, e.g., `https://k8scenter.example.com`. Required for both iOS and Android release builds; CI fails upfront when missing. |

Once all secrets are set, the next push to `main` that touches `mobile/` triggers `deploy_ios` and `deploy_android`. The first build takes ~10 minutes.

## Android setup

### 1. Google Play Console

Create a developer account at <https://play.google.com/console/>. $25 one-time. Create a new app with package `io.kubecenter.kubecenter`.

### 2. Generate an upload signing key

```bash
keytool -genkey -v -keystore upload-keystore.jks -keyalg RSA -keysize 2048 \
  -validity 10000 -alias upload
```

Keep the keystore + password safe. CI signs every release AAB with this upload key — finding P1-5 of the 2026-05-22 security audit required removing the legacy debug-signing fallback. CI will refuse to ship a release AAB if any of the four `ANDROID_UPLOAD_*` secrets is absent (see step 5 below).

### 3. Create a Play Console service account

In Play Console → Setup → API access → Choose existing GCP project (or create new) → Create new service account → Done. Grant it the "Release manager" role on your app.

Download the JSON key. Set the entire JSON as the `PLAY_SERVICE_ACCOUNT_JSON` secret.

### 4. Grant the service account permission

In Play Console → Users and permissions → Invite the service account email → grant **Release to production** + **Release to testing tracks** for the k8sCenter app.

### 5. First manual upload

Play requires a first manual AAB upload before the API will accept programmatic ones. From a developer machine:

```bash
cd mobile
flutter build appbundle --release \
  --dart-define=BACKEND_URL=https://k8scenter.example.com
```

Omitting `--dart-define=BACKEND_URL=...` causes the release build to abort with `StateError: BACKEND_URL is required in release/profile builds` (finding P1-4).

Upload `build/app/outputs/bundle/release/app-release.aab` to Play Console → Internal testing → Create new release. After this completes, subsequent uploads via Fastlane work.

## Universal Links

Optional. Without these, deep links use the `k8scenter://` custom scheme — works fine, just less polished than tapping a real HTTPS URL and having it open the app directly.

> **Single source of truth.** When enabling Universal Links, the same domain must be set in **all four** of:
> 1. `mobile.universalLinkDomain` (Helm values, this section step 2) — chooses where the well-known files are served
> 2. `UNIVERSAL_LINK_HOST` (GitHub Actions secret, used by both `deploy_ios` and `deploy_android`) — substituted into the iOS entitlement and Android manifest at build time
> 3. iOS Xcode entitlement (this section step 4) — required for the Apple AASA verification round-trip
> 4. Apple Developer Portal → App ID → Capabilities → Associated Domains
>
> Mismatches silently break Universal Link verification. The mobile-ci.yml `deploy_check` job emits a `::notice::` when one of (1)/(2) is set but the other is empty.

### 1. Choose a host

Operators typically use the same domain that hosts the k8sCenter web frontend (`kubecenter.example.com`). The host must be **HTTPS-served** and reachable from the public internet — Apple and Google both fetch the well-known files from outside your cluster. The Helm chart fail-fasts when `mobile.universalLinkDomain` is set without `ingress.tls`.

### 2. Set Helm values

```yaml
mobile:
  universalLinkDomain: "kubecenter.example.com"
  iosTeamId: "ABCDE12345"
  iosBundleId: "io.kubecenter.kubecenter"
  androidPackageName: "io.kubecenter.kubecenter"
  androidSha256CertFingerprint: "AB:CD:EF:01:23:..."
```

Get `iosTeamId` from Apple Developer → Membership. Get the Android SHA-256 from Play Console → Setup → App integrity → App signing key certificate (or `keytool -list -v -keystore <jks>` for the upload key).

### 3. Apply the chart

```bash
helm upgrade kubecenter helm/kubecenter --values <your-values.yaml>
```

The chart renders an Ingress route at `<domain>/.well-known/apple-app-site-association` and `<domain>/.well-known/assetlinks.json`. Verify both serve `application/json` with `curl -i`.

### 4. Wire the iOS entitlement

The repo ships `mobile/ios/Runner/Runner.entitlements` already wired into `Runner.xcodeproj`'s `CODE_SIGN_ENTITLEMENTS` build setting (Debug, Release, and Profile). The default file ships with an **empty** `com.apple.developer.associated-domains` array — the CI's `deploy_ios` job substitutes the operator's domain at build time via:

```bash
plutil -replace com.apple.developer.associated-domains \
  -json '["applinks:'"$UNIVERSAL_LINK_HOST"'"]' \
  mobile/ios/Runner/Runner.entitlements
```

For local Xcode archives without CI, open `mobile/ios/Runner.xcworkspace` → Runner target → Signing & Capabilities → Associated Domains and add `applinks:<your-domain>` directly. Don't commit your domain back to the repo — keep the empty array as the default.

### 5. Set the Android Gradle property

In CI, set the `UNIVERSAL_LINK_HOST` GitHub Actions secret. The `deploy_android` workflow forwards it to Gradle, which substitutes the manifest placeholder. For local builds:

```bash
flutter build appbundle --release \
  --dart-define=UNIVERSAL_LINK_HOST=kubecenter.example.com \
  --dart-define=BACKEND_URL=https://k8scenter.example.com \
  -- -PuniversalLinkHost=kubecenter.example.com
```

### 6. Verify

Send yourself a Universal Link via Messages or email. Tapping should open the app directly. If it opens Safari/Chrome instead, use Xcode's Console / `adb logcat` to inspect the verification failure — the most common cause is the well-known file returning the wrong content-type.

## FCM (push notifications)

PR-1f's FCM registration is a no-op when the operator hasn't dropped in `google-services.json` (Android) and `GoogleService-Info.plist` (iOS). To enable push:

1. Create a Firebase project at <https://console.firebase.google.com>. Add an iOS app and an Android app with package name `io.kubecenter.kubecenter`.
2. Download the config files. Drop them into `mobile/ios/Runner/GoogleService-Info.plist` and `mobile/android/app/google-services.json`.
3. Generate an APNs auth key in Apple Developer → Keys → +. Upload it to Firebase → Project settings → Cloud Messaging → Apple app config.
4. Set the backend's `KUBECENTER_FCM_CREDENTIALS_PATH` env var to a service-account JSON downloaded from Firebase → Project settings → Service accounts.
5. Restart the backend; the next push channel test (`POST /api/v1/notifications/channels/<id>/test`) delivers to registered devices.

## Rotating secrets

The pipeline depends on several long-lived secrets that operators rotate periodically. The order matters for two of them:

### `MATCH_PASSWORD` (iOS cert encryption)

This password decrypts the cert + provisioning-profile bundle in your private `match` repo. Rotating it requires re-encrypting the bundle **before** updating the GitHub Actions secret, otherwise CI's `deploy_ios` permanently fails to decrypt:

1. From a developer Mac with the team's Apple ID logged in:
   ```bash
   cd mobile/fastlane
   bundle exec fastlane match change_password
   ```
   Enter the old password, then the new one. Fastlane re-encrypts every cert in the match repo and pushes back to git.
2. Verify the updated commit appears in the match repo on GitHub.
3. Update the `MATCH_PASSWORD` secret in GitHub Actions Settings → Secrets and variables → Actions.
4. Re-run `deploy_ios` to confirm CI can decrypt with the new password.

If you update the secret first, every CI run fails with `OpenSSL::Cipher::CipherError` until you complete step 1 + 2.

### `APPSTORE_CONNECT_API_KEY` (TestFlight upload auth)

App Store Connect API keys are revocable from Users and Access → Integrations. When rotating:

1. Generate a new key with the App Manager role.
2. Update the `APPSTORE_CONNECT_API_KEY` secret with the new JSON blob (same shape as initial setup).
3. Revoke the old key in App Store Connect.
4. The next push to `main` uses the new key.

If the old key expires or is revoked before step 2, `deploy_ios` fails with HTTP 401 from App Store Connect.

### `PLAY_SERVICE_ACCOUNT_JSON` (Play Internal upload auth)

Service-account JSON keys can be rotated in Google Cloud Console → IAM & Admin → Service Accounts → Keys. Update the GitHub secret with the new key JSON; the old key continues to work until you delete it. Rotation is non-blocking.

### `MATCH_GIT_URL`

Changing the cert-storage repo URL is a one-time migration: clone the old repo, push to the new one (preserving encrypted blobs), then update the secret. There's no rotation friction for this one.

## Smoke checklist

After the first end-to-end deploy:

- [ ] Push a commit to `main` that touches `mobile/`. Watch the `deploy_ios` and `deploy_android` jobs in GitHub Actions. Both should complete in <15 minutes.
- [ ] TestFlight: confirm the new build appears in App Store Connect → TestFlight within 10 minutes of upload.
- [ ] Play Internal: confirm the new release appears in Play Console → Internal testing → Releases within 10 minutes.
- [ ] Tap a Universal Link to a known resource (e.g., a Pod in the homelab). The app should open to the resource detail without bouncing through Safari/Chrome.
- [ ] Send a test push (`POST /api/v1/notifications/channels/<id>/test`) targeting your registered device. Confirm the notification arrives and tapping it deep-links to the resource.

When all five tick, the internal-beta pipeline is operational.

## Public-store promotion

The internal-beta pipeline ships every push to `main`. **Public-store promotion is separate** — manual `workflow_dispatch` against `mobile-ci.yml`, never push-triggered. Apple's 24h–7d review SLA and Play's 10% staged rollout are deliberately decoupled from CI lifecycle.

The `promote_ios` and `promote_android` Fastlane lanes (`mobile/fastlane/Fastfile`) own the App Store + Play Store production submission. This section is the bring-up runbook for first-time public launch and the recurring step-by-step for subsequent releases.

### Promotion prerequisite tracker

| Gate | Required to | Lead time | First-time only? |
|---|---|---|---|
| Apple Developer Program enrollment | Submit to App Store | 24–48h Apple approval | Yes |
| Play Console first manual AAB upload | Promote Internal → Production | Same-day | Yes |
| Privacy policy URL live (`https://kubecenter.io/privacy`) | Submission accepted by either store | Same-day | Yes |
| App Privacy questionnaire (App Store Connect) | App Store review approved | Same-day | Yes |
| Play Data Safety form (Play Console) | Play submission accepted | Same-day | Yes |
| iOS screenshots populated under `mobile/fastlane/screenshots/en-US/` | `promote_ios` does not fail at submit-for-review | Same-day (real device captures) | Recurring per major release |
| Sentry DSN provisioned + tested | Crash reporting works once Settings toggle is enabled | Same-day | Yes |
| Apple Developer team_id matches Appfile | `match` succeeds in CI | Same-day | Yes |
| App Store Review Notes field populated | Avoids guideline 5.1.2 holds | Same-day | Yes |

All nine gates must clear before `promote_ios` or `promote_android` will succeed end-to-end. The lanes themselves enforce gate-3, gate-6, gate-7, and gate-8 with explicit failure messages; the rest are operator responsibilities.

### iOS public launch (first time)

1. **Apple Developer Program enrollment** — see `iOS setup → 1. Apple Developer Program enrollment`. Required gate before any of the steps below.
2. **App Store Connect listing record** — see `iOS setup → 2. Create the App Store Connect record`. The Bundle ID must match `io.kubecenter.kubecenter` exactly.
3. **App Privacy questionnaire** — App Store Connect → App Privacy → fill per the table in `mobile/docs/APP_PRIVACY.md`. Apple validates submissions against this; mismatches between actual data collection and the questionnaire cause reviewer rejections.
4. **Privacy policy URL** — publish a page at `https://kubecenter.io/privacy`. The page content is checked into `frontend/routes/privacy.tsx`; deploy the docs site (or wire the privacy route into whatever hosts `kubecenter.io`) and confirm `curl -sI https://kubecenter.io/privacy` returns `200 OK` before continuing.
5. **App Store Review Information notes** — App Store Connect → App Information → App Store Review Information → Notes field. Paste the "What this app is" paragraph from `mobile/docs/APP_PRIVACY.md` verbatim. Pre-empts the most likely reviewer confusion about Kubernetes-credentials handling.
6. **Screenshots** — capture 5 surfaces per device class on a real iPhone 14 Pro Max + real iPad Pro 12.9, both light and dark themes. Save under `mobile/fastlane/screenshots/en-US/` following the [fastlane deliver layout](https://docs.fastlane.tools/actions/deliver/#screenshots). Verify each screenshot contains no homelab cluster names or other identifiable data before commit.
7. **Branded app icon** — replace the placeholder Flutter icons under `mobile/ios/Runner/Assets.xcassets/AppIcon.appiconset/` with branded 1024×1024 master + auto-generated sizes. Tools: `fastlane appicon` or any icon generator.
8. **Sentry DSN** — provision a project at <https://sentry.io>, set the `SENTRY_DSN_MOBILE` GitHub Actions secret. The next `beta_ios` build picks it up via `--dart-define=SENTRY_DSN=...`. Send a synthetic crash from a release build and inspect the Sentry payload to confirm the scrubber catches PII before going live.
9. **Run `beta_ios`** at least once after gates 1–8 clear, so TestFlight has a current build to submit. The TestFlight build IS the binary that gets submitted to App Store.
10. **Run `promote_ios`** via `workflow_dispatch` — pushes metadata, screenshots, and submits the latest TestFlight build for App Store review. The lane fails loudly if screenshots/en-US/ is empty.
11. **Wait** for App Store review. SLA: 24h typical, 7d worst case. Rejection feedback (if any) lands in App Store Connect → Resolution Center. Address feedback in a follow-up PR; re-run `promote_ios`.
12. **Phased release** — once approved, App Store Connect → Pricing and Availability → manually graduate from "Phased Release" to "Available" after the staged rollout signals are clean.

### Android public launch (first time)

1. **Play Console developer account** — see `Android setup → 1. Google Play Console`. Same record as Internal-track.
2. **First manual AAB upload to Internal track** — see `Android setup → 5. First manual upload`. Play won't accept programmatic promotion before this is done.
3. **Data Safety form** — Play Console → Policy → App Content → Data Safety → fill per the Play mapping table in `mobile/docs/APP_PRIVACY.md`.
4. **Privacy policy URL** — same as iOS step 4. Play validates the URL at submission time; broken URLs reject with reason "Privacy policy".
5. **Branded app icon + adaptive layers** — replace the placeholder under `mobile/android/app/src/main/res/mipmap-*/ic_launcher.png` with branded foreground (432×432) + background color. Use `flutter pub run flutter_launcher_icons` or any generator.
6. **Sentry DSN** — same provisioning as iOS step 8. Same `SENTRY_DSN_MOBILE` secret; reused across platforms.
7. **Run `beta_android`** at least once after gates 1–6 clear, so the Internal track has a current build to promote.
8. **Run `promote_android`** via `workflow_dispatch` — promotes the most recent Internal AAB to Production at 10% staged rollout. No re-build, no re-upload.
9. **Monitor crash + ANR rate** for 48h via Play Console → Quality → Android vitals. Acceptable bands: crash rate <1%, ANR rate <0.5%.
10. **Graduate to 100%** — Play Console → Production → expand the active release → set rollout to 100%. Optional staged graduations at 25% / 50% / 75% if the team prefers slower rollout.

### Subsequent public releases (after first launch)

After the bring-up is done, each subsequent release reduces to:

| Step | iOS | Android |
|---|---|---|
| 1 | Update `mobile/fastlane/metadata/en-US/release_notes.txt` | Update `mobile/fastlane/metadata/android/en-US/release_notes.txt` |
| 2 | Capture fresh screenshots if UI changed | Capture fresh phoneScreenshots/ if UI changed |
| 3 | Merge to `main` → `beta_ios` ships an updated build to TestFlight | Merge to `main` → `beta_android` ships an updated AAB to Internal |
| 4 | `workflow_dispatch` → `promote_ios` | `workflow_dispatch` → `promote_android` |
| 5 | Wait for App Store review (24h–7d), then phased release | Monitor 48h on 10% rollout, then graduate to 100% |

Apple reviews every release. Play reviews are typically waived for established apps with clean rollout history.

### Troubleshooting

**`promote_ios` fails with "missing screenshots"**: `mobile/fastlane/screenshots/en-US/` is empty or missing required device-class subdirectories. Apple rejects submissions without screenshots; the lane defends this gate intentionally. Populate screenshots from a real-device capture, then re-run.

**`promote_ios` fails with HTTP 401 from App Store Connect**: The API key is expired, revoked, or has insufficient role. Generate a new key with App Manager role; update the `APPSTORE_CONNECT_API_KEY` secret. See `Rotating secrets → APPSTORE_CONNECT_API_KEY`.

**`promote_ios` fails with "no TestFlight build to submit"**: Run `beta_ios` first to publish a build to TestFlight. `promote_ios` submits the latest TestFlight build; it doesn't build a new one.

**`promote_ios` rejected by App Store review with guideline 5.1.2 (Data Collection and Storage)**: The Review Information Notes field is missing or doesn't explain Kubernetes-credentials handling. Paste the "What this app is" paragraph from `mobile/docs/APP_PRIVACY.md` into App Store Connect → App Information → Review Information → Notes. Re-run `promote_ios`.

**`promote_android` fails with HTTP 404**: The lane's rescue block already prints the three most likely causes (no AAB on Internal track, missing first manual upload, service-account permissions). Address the relevant one and re-run.

**`promote_android` rejected for "Privacy policy"**: Either `https://kubecenter.io/privacy` isn't reachable, or the Data Safety form contradicts actual data collection. `curl -sI https://kubecenter.io/privacy` must return 200; re-fill the Data Safety form against `mobile/docs/APP_PRIVACY.md`.

**App Store Connect "App Privacy" section won't save**: Apple validates the questionnaire against the actual data the binary collects (their static analysis looks at SDK imports). If you toggle "Collects no data" but the binary imports a Sentry SDK, the form rejects. Either remove the SDK (if not used) or admit collection.

**Sentry events arrive in production with PII visible**: The scrubber didn't catch it. Patch `mobile/lib/observability/pii_scrubber.dart` and ship a hotfix release before any further public rollout. The Sentry-side compensating controls (release-filter + rate-limit) mitigate but don't replace the on-device scrub.

**A Universal Link tap opens Safari instead of the app on a real device**: AASA verification failed. Check `curl -sI https://<universal-link-host>/.well-known/apple-app-site-association` returns `application/json`. See `Universal Links → 6. Verify`.

### Rollback

If a public release introduces a regression:

- **iOS**: App Store Connect → My Apps → version → "Remove from Sale". Reversible within minutes. Ship the fix as a patch release; re-run `promote_ios`.
- **Android**: Play Console → Production → active release → "Halt rollout". The stop is immediate for users not yet served; users who already received the bad build keep it. Ship the fix as a patch release; re-run `promote_android` at a fresh 10% rollout. The halted release stays in the version history.

Both rollback paths are reversible. Neither requires re-submission for review (the rollback itself doesn't change the app; only the distribution gate).

### Public-launch smoke

After both promotions land in production:

- [ ] App Store listing renders at `https://apps.apple.com/app/k8scenter` with screenshots, description, age rating 4+.
- [ ] Play Store listing renders at `https://play.google.com/store/apps/details?id=io.kubecenter.kubecenter` with screenshots, full description.
- [ ] Install the App Store release on a clean iPhone (no TestFlight). Sign in to a real k8sCenter server. Tail Pod logs. Confirm core flow works end-to-end.
- [ ] Install the Play Store release on a clean Android device (no Internal-track access). Same end-to-end sign-in + log-tail flow.
- [ ] Sentry receives a synthetic crash event from one of the installed devices (toggle Crash Reporting in Settings → trigger a test exception). Confirm PII scrub worked.
- [ ] Push notification delivered to one of the installed devices via `POST /api/v1/notifications/channels/<id>/test`. Tap → deep-links to the resource.

When all six tick, the public-store launch is operational.
