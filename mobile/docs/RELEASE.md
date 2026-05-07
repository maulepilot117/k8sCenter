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

| Secret | Value |
|---|---|
| `MATCH_GIT_URL` | `https://github.com/your-org/k8scenter-mobile-certs.git` |
| `MATCH_PASSWORD` | The passphrase set during `match init` |
| `APPSTORE_CONNECT_API_KEY` | The JSON blob from step 3 |
| `APPLE_ID` | Apple ID email used for the dev account |
| `APPLE_TEAM_ID` | 10-char team ID from Apple Developer → Membership |

Once all five are set, the next push to `main` that touches `mobile/` triggers `deploy_ios`. The first build takes ~10 minutes.

## Android setup

### 1. Google Play Console

Create a developer account at <https://play.google.com/console/>. $25 one-time. Create a new app with package `io.kubecenter.kubecenter`.

### 2. Generate an upload signing key

```bash
keytool -genkey -v -keystore upload-keystore.jks -keyalg RSA -keysize 2048 \
  -validity 10000 -alias upload
```

Keep the keystore + password safe. The CI workflow doesn't currently sign release AABs (it uses Gradle's debug signing for first deploy); add `signingConfigs` to `mobile/android/app/build.gradle.kts` once the keystore is available, and provide it via a `KEYSTORE_BASE64` secret.

### 3. Create a Play Console service account

In Play Console → Setup → API access → Choose existing GCP project (or create new) → Create new service account → Done. Grant it the "Release manager" role on your app.

Download the JSON key. Set the entire JSON as the `PLAY_SERVICE_ACCOUNT_JSON` secret.

### 4. Grant the service account permission

In Play Console → Users and permissions → Invite the service account email → grant **Release to production** + **Release to testing tracks** for the k8sCenter app.

### 5. First manual upload

Play requires a first manual AAB upload before the API will accept programmatic ones. From a developer machine:

```bash
cd mobile
flutter build appbundle --release
```

Upload `build/app/outputs/bundle/release/app-release.aab` to Play Console → Internal testing → Create new release. After this completes, subsequent uploads via Fastlane work.

## Universal Links

Optional. Without these, deep links use the `k8scenter://` custom scheme — works fine, just less polished than tapping a real HTTPS URL and having it open the app directly.

### 1. Choose a host

Operators typically use the same domain that hosts the k8sCenter web frontend (`kubecenter.example.com`). The host must be HTTPS-served and reachable from the public internet — Apple and Google both fetch the well-known files from outside your cluster.

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

In Xcode, open `mobile/ios/Runner.xcworkspace` → Runner target → Signing & Capabilities → + Capability → Associated Domains. Add `applinks:<your-domain>`. Commit the resulting changes to `Runner.entitlements` and `Runner.xcodeproj/project.pbxproj`.

### 5. Set the Android Gradle property

In CI, set the `UNIVERSAL_LINK_HOST` GitHub Actions secret. The `deploy_android` workflow forwards it to Gradle, which substitutes the manifest placeholder. For local builds:

```bash
flutter build appbundle --release \
  --dart-define=UNIVERSAL_LINK_HOST=kubecenter.example.com \
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

## Smoke checklist

After the first end-to-end deploy:

- [ ] Push a commit to `main` that touches `mobile/`. Watch the `deploy_ios` and `deploy_android` jobs in GitHub Actions. Both should complete in <15 minutes.
- [ ] TestFlight: confirm the new build appears in App Store Connect → TestFlight within 10 minutes of upload.
- [ ] Play Internal: confirm the new release appears in Play Console → Internal testing → Releases within 10 minutes.
- [ ] Tap a Universal Link to a known resource (e.g., a Pod in the homelab). The app should open to the resource detail without bouncing through Safari/Chrome.
- [ ] Send a test push (`POST /api/v1/notifications/channels/<id>/test`) targeting your registered device. Confirm the notification arrives and tapping it deep-links to the resource.

When all five tick, the mobile pipeline is operational.
