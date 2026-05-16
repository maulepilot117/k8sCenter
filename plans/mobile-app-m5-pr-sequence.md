---
title: "Mobile M5 PR Sequence — Polish + Public Store Launch + OIDC + Write Parity"
type: feat
status: active
date: 2026-05-16
origin: plans/mobile-app.md
---

# Mobile M5 PR Sequence — Polish + Public Store Launch + OIDC + Write Parity

## Summary

Land M5 of `plans/mobile-app.md` as ten reviewable PRs (PR-5a → PR-5j) that close the remaining oncall gaps and promote the Flutter app from internal beta to public stores. PR-5a stands up Settings + Sentry scaffolding + `SecureScreenMixin` + a11y test helpers — the shared primitives every later PR depends on. PR-5b adds the backend body-mode OIDC token exchange (mirrors PR-0's refresh body-mode pattern). PR-5c wires the mobile OIDC flow via `flutter_custom_tabs` + client-generated PKCE, hooking into the existing provider-list seam on the login screen. PR-5d ships secret-screen screenshot suppression (Android `FLAG_SECURE` + iOS `AppLifecycleState`-driven blur cover) — the PR-1d #17 carryover. PR-5e closes mobile write parity by enabling Force Sync (renaming the misnamed "Drift Revert" mobile button to match web/backend) and shipping the ESO Bulk Refresh modal sheet — both local-cluster only. PR-5f bundles M4 polish carryovers: pinch-to-zoom on `KubeLineChart`, compliance history time-range picker, admin "all namespaces" LogQL mode, per-resource service-name autoderivation for golden signals on Pod/Deployment screens. PR-5g ships the first-launch 3-card onboarding tour. PR-5h is the WCAG 2.2 AA accessibility pass across all M1–M4 surfaces. PR-5i is the performance pass — fixes the `DataTable2` eager-row materialization bug, profiles cold start, documents the baseline. PR-5j ships public-store metadata, screenshots, app icons, Fastlane `promote_*` lanes for App Store + Play Store production, privacy policy, and the final integration smoke. One small backend addition (OIDC body-mode endpoint); everything else is mobile-only.

---

## Problem Frame

M4 closed the read-side observability + CRD-detail parity gap. The remaining mobile gaps before public-store launch are:

- **OIDC users cannot sign in on mobile.** The cookie-based OIDC token exchange the web uses works in a normal browser (cookies cross the same-origin boundary) but not in `flutter_custom_tabs`: the in-app browser opens a separate process whose cookie jar is invisible to the embedded Dio client. OIDC-only operators (Authelia, Keycloak, Google Workspace, Azure AD) are stuck on desktop.
- **Two ESO write actions render as disabled placeholders.** `DisabledRevertDriftButton` (mobile/lib/features/eso/eso_widgets.dart:360-389) and bulk-refresh trigger both show "Use desktop" tooltips. The backend endpoints exist (`/force-sync` is already shipped from web phase 14; bulk-refresh ditto). Mobile parity is a UI wiring exercise, not new server code.
- **Revealed Secret plaintext is visible in the OS app-switcher snapshot.** No `FLAG_SECURE` on Android, no `AppLifecycleState` blur cover on iOS. Surfaced by PR-1d code review (#17), explicitly routed to M5 polish.
- **Public-store launch infra is incomplete.** No `mobile/fastlane/metadata/`, no screenshots, no `promote_*` lanes. Apple Developer enrollment and the manual Play AAB upload have known operational pitfalls (documented in `mobile/docs/RELEASE.md`).
- **No accessibility coverage.** Grep across `mobile/lib/` for `Semantics(`, `semanticsLabel`, `ExcludeSemantics`, `MergeSemantics`, `excludeFromSemantics` returns zero matches. No `meetsGuideline(textContrastGuideline)` tests. Sighted-only experience.
- **No crash visibility.** When the public-store version crashes, we hear about it via a one-star review or a support ticket two days later. No telemetry signal.
- **Public-store strangers land cold.** Internal-beta users know what k8sCenter is; public-store discoverers need a 15-second orientation.
- **M4 deferred a small set of polish items.** Chart pinch-to-zoom, compliance history time-range picker, admin "all namespaces" LogQL mode, per-resource service-name autoderivation for golden signals on Pod/Deployment screens. Each was deferred individually; together they form a coherent polish bundle.
- **`ResourceTable` claims lazy row materialization but does not deliver.** The code comment says "lazy-builds rows as they scroll into view"; the implementation passes `rows: [for ... ]` to `DataTable2`, which materializes eagerly. On a 6000-row vulnerability list this is the primary jank source. Senior-dev-override: fix proactively rather than ship public with a latent perf cliff.

Each gap blocks a specific operator persona: OIDC blocks the corporate user, write parity blocks the operator who can't reach a desktop, FLAG_SECURE blocks the security-conscious user, accessibility blocks the disabled user, and the store launch blocks every user who isn't already in the internal-beta TestFlight ring.

**Scope-expansion note:** The origin `plans/mobile-app.md` framed M5 as a 2–3 week milestone covering accessibility + perf + Sentry + store launch only. The user confirmed inclusion of OIDC mobile flow + ESO write parity + first-launch onboarding at the Phase 0.7 scope summary. These add scope significantly. Realistic M5 timeline: **4–5 weeks under parallel execution** (PRs 5a/5b/5c/5d running concurrent with 5e/5f/5g once 5a lands, 5h + 5i after), or **6–8 weeks under sequential execution** with a single developer. PR-5h alone is wide (40 screens). PR-5b is new backend auth requiring full security review. PR-5e has a non-trivial endpoint-discovery prerequisite. Phased Delivery section below defines the parallelism model explicitly. The 4–5 week estimate is honest only if parallelism is real.

---

## Requirements

- **R1. OIDC mobile login parity.** Operators with OIDC-only identity providers (Authelia, Keycloak, Google Workspace, Azure AD) can sign in on iOS + Android. Mobile generates the PKCE verifier client-side, generates a random nonce, opens the IdP authorization URL in `flutter_custom_tabs` (Android) / `SFSafariViewController` (iOS) with `code_challenge=...&code_challenge_method=S256&nonce=...&state=...`, intercepts the redirect to the redirect URI (see Key Technical Decisions for the scheme decision), and exchanges the code via a new backend body-mode endpoint that returns the JWT pair in the response body (not cookies). State parameter validated client-side for CSRF protection; nonce echoed to backend in the exchange body and validated against the ID token claim for ID-token replay protection.
- **R2. ESO Force Sync from mobile, on local cluster only.** The mobile "Drift Revert" button is renamed to **Force Sync** (web/backend terminology) and wired to `POST /v1/externalsecrets/externalsecrets/{ns}/{name}/force-sync`. UI disabled with explanatory tooltip when the active cluster is not local (backend returns 501 for remote). Type-to-confirm via the existing `confirm_sheet.dart` pattern.
- **R3. ESO Bulk Refresh from mobile, on local cluster only.** Three scope variants matching the actual backend endpoints (`backend/internal/server/routes.go:654-664`): refresh-all-for-store (`/stores/{ns}/{name}/refresh-scope` → `/stores/{ns}/{name}/refresh-all`), refresh-all-for-clusterstore (`/clusterstores/{name}/refresh-scope` → `/clusterstores/{name}/refresh-all`), refresh-all-in-namespace (`/refresh-namespace/{namespace}/refresh-scope` → `/refresh-namespace/{namespace}`). Each variant follows a four-phase modal sheet flow: scope-pick (mobile-only — pick variant + identifier) → scope-load (GET the variant's `refresh-scope` endpoint to count affected ExternalSecrets) → confirm (type-to-confirm with count + per-namespace breakdown) → submit (POST the variant's submit endpoint) → progress-poll (2s interval against `/bulk-refresh-jobs/{jobId}`). Handles the 409 `active_job_exists` branch by attaching to the existing job. Mirrors `frontend/islands/ESOBulkRefreshDialog.tsx`'s endpoint selection logic.
- **R4. Secret screen screenshot suppression.** `SecretDetailScreen` activates Android `FLAG_SECURE` and pushes an iOS blur overlay when the app enters `AppLifecycleState.inactive` or `AppLifecycleState.paused`, but only when at least one Secret key has been revealed. When the screen has no revealed keys, suppression is off (k8s YAML metadata is not sensitive). Abstracted as `SecureScreenMixin` so other plaintext-reveal surfaces can adopt it later.
- **R5. Sentry crash reporting, opt-in, default off.** New Settings screen exposes a `SwitchListTile` for "Send crash reports". When toggled on, `sentry_flutter` initialises with PII scrubbing: namespace names, resource names, secret names, user identity (`User.username`/email), and FCM device tokens are stripped from breadcrumbs and event payloads. Off by default; flag persisted in `shared_preferences`. `sentry_flutter` is only imported when the flag is true (no Sentry SDK calls at all in the off path).
- **R6. WCAG 2.2 AA accessibility pass across all M1–M4 surfaces.** Concrete targets: (a) every status chip, icon button, and pill indicator carries a `semanticsLabel`; (b) `meetsGuideline(textContrastGuideline)` passes across all 7 themes for the `textPrimary/bgBase` and `textSecondary/bgSurface` pairs; (c) TalkBack/VoiceOver traversal order is sensible on the login screen and the resource detail scaffold (the two most-used screens); (d) dynamic type scaling up to `MediaQuery.textScaleFactor == 2.0` does not clip critical UI on phone form factor.
- **R7. Performance pass — fix DataTable2 eager-row bug + cold-start baseline.** Replace `rows: [for ...]` in `mobile/lib/widgets/resource_table.dart` with a virtualized variant over a `KubeDataTableSource` (subclass of `DataTableSource`). **Two surface treatments — decide per surface at PR-5i time:** (a) the 6000-row vulnerability list uses `DataTable2.fromDataSource(...)` for continuous virtualized scroll (no pagination — paginating a vuln list is a UX regression); (b) smaller lists may keep `PaginatedDataTable2(source: KubeDataTableSource(...))` if pagination is the existing UX. The `KubeDataTableSource` abstraction supports both. **Pre-implementation verification:** confirm whether the vulnerability list screen (`mobile/lib/features/scanning/*`) currently uses `ResourceTable` (DataTable2-based) or already uses `ListView.builder`. If it already uses `ListView.builder`, the M5 fix scope shrinks to `ResourceTable` only. Establish cold-start, scroll-jank, and frame-budget baselines in `mobile/docs/PERFORMANCE.md`. Optimisations beyond the table fix are discovered at PR time and applied surgically.
- **R8. First-launch onboarding tour.** Three swipeable cards: (1) **What k8sCenter Mobile is** — explains it's an oncall companion for an existing self-hosted k8sCenter backend; if you don't have one, links to `kubecenter.io/install`. (2) **Cluster pin** — short orientation on the cluster picker + adding a cluster. (3) **Notifications** — opt-in to push alerts (links to OS permission prompt). Shown exactly once per device. Flag `onboarded_v1` in `shared_preferences`. Internal-beta users who upgrade do **not** see it: the existing presence of a secure-storage refresh token at app start sets the flag silently before the router decides. Skippable on each card. **Biometric unlock is NOT in the tour** — M5 does not ship biometric authentication (no `local_auth` dependency added, no Settings → Security surface created), so the tour cannot advertise it; biometric onboarding belongs in a future milestone where the feature actually exists.
- **R9. M4 polish carryovers.** Chart pinch-to-zoom on `KubeLineChart` (stateful `_ZoomState` over `minX/maxX`, GestureDetector for `onScaleUpdate`, double-tap to reset). Compliance history time-range picker (uses the existing `TimeRangePicker` widget). Admin "all namespaces" LogQL mode (admin-only checkbox toggles the namespace dropdown to a "no filter" sentinel). Per-resource service-name autoderivation for golden signals on Pod/Deployment screens (find Services whose `selector` matches the resource's labels via the existing resource cache).
- **R10. Public-store launch infrastructure.** `mobile/fastlane/metadata/en-US/` for App Store Connect (name, subtitle, description, keywords, release_notes, privacy_url, support_url, marketing_url). `mobile/fastlane/metadata/android/en-US/` for Play Console (title, short_description, full_description, video). `mobile/fastlane/screenshots/` populated with phone + tablet, light + dark screenshots from real device runs. `promote_ios` lane in `Fastfile` (TestFlight Internal → submit_for_review). `promote_android` lane (Internal → Closed → Production via `upload_to_play_store(track: 'production')`). App icons (iOS 1024×1024 + Android adaptive icon foreground/background layers). Launch splash screen. Privacy policy hosted at a public URL (likely `kubecenter.io/privacy`). App Privacy questionnaire prep doc.
- **R11. Web/Dart isomorphism extends.** Every new mobile surface mirrors an existing web shape:
  - OIDC mobile flow ↔ `frontend/lib/auth.ts:handleOIDCCallback` (mobile is the same handshake, body-mode instead of cookie).
  - Force Sync ↔ backend `/force-sync` endpoint (same wire contract).
  - Bulk Refresh ↔ `ESOBulkRefreshDialog.tsx` (web has 3 phases scope-load → confirm → submit+poll; mobile adds an extra **scope-pick** phase up front, making it 4 phases — see PR-5e and the High-Level Technical Design diagram).
  - Settings screen ↔ web settings page surfaces where applicable (Sentry opt-in is mobile-only; theme picker already shared).
  - Drift in either direction counts as a bug.
- **R12. Cluster-pinning discipline carries.** Every new write action (Force Sync, Bulk Refresh) and every new read controller (the polish carryovers in PR-5f) keys on `clusterId`, threads `clusterIdOverride` through repos, and re-checks pin at result arrival via `RefreshableController._clusterStillPinned(_PinPhase.postEmission)`. ESO writes additionally gate on local-cluster (`activeClusterProvider == 'local'`); UI shows a "Use desktop for remote-cluster ESO writes" tooltip otherwise.

**Origin actors (informal — origin doc uses prose, not formal A-IDs):** Oncall operator (the primary M1–M4 audience) and cluster admin (the only audience for compliance history time-range + admin "all namespaces" LogQL).

**Origin flows:**
- F1 (oncall opens workload from notification → tap "Force Sync" → confirm → see drift cleared without leaving phone).
- F2 (OIDC user signs in on mobile for the first time).
- F3 (security-conscious user reveals a Secret → switches apps → app-switcher snapshot shows blurred placeholder, not the plaintext).
- F4 (public-store user installs the app → 3-card onboarding orients them → first cluster registered).
- F5 (TalkBack user navigates the login screen and resource detail scaffold).

---

## Scope Boundaries

- **Theme parity at build time** continues per `make check-themes`. M5 adds contrast-ratio audit tests that read the generated hex values from `themes.g.dart` — does not change the generator.
- **No new backend except for the OIDC body-mode endpoint.** Every other M5 feature uses an existing endpoint or is mobile-only.
- **Read-only posture stays the default.** M5 adds exactly two write actions (Force Sync + Bulk Refresh). Any other previously-disabled action (e.g. Velero restore that may surface in M5 testing) stays disabled with its existing tooltip.
- **Public-store launch is iOS + Android only.** No macOS, no Windows store, no Linux desktop. Apple Watch / Wear OS remain out of scope per `mobile-app.md`.
- **Sentry is opt-in, default off.** No PII in breadcrumbs or event payloads. No telemetry beyond what `sentry_flutter` provides natively. No custom analytics — Sentry is for crashes only, not user behaviour.
- **Accessibility scope is M1–M4 surfaces only.** New M5 surfaces (Settings, Onboarding, ESO write modals, OIDC button) ship with a11y annotations in their own PRs (not deferred to PR-5h).
- **Performance pass is baseline-establishing + the known DataTable2 fix.** Discovery-time optimisations are bounded to what the profile reveals; M5 does not commit to optimising features that profile clean.
- **OIDC mobile flow uses `flutter_custom_tabs` + manual PKCE.** `flutter_appauth` was considered (handles PKCE + state automatically); rejected because (a) it requires platform-specific config that conflicts with the existing FCM + Universal Links setup, (b) manual PKCE is ~30 lines of Dart against `package:crypto`, (c) the backend already implements the IdP side of PKCE for web — mobile inherits the same library and config.
- **No live mobile testing on iOS without Apple Developer enrollment.** PR-5j depends on enrollment being complete (24–48h lead time). If enrollment isn't ready, PR-5j ships the metadata + screenshots + lanes but does NOT submit for review.

### Deferred to Follow-Up Work

- **OIDC refresh-token rotation parity audit (PROMOTED to PR-5b — must resolve before merge, not at PR-5c test time).** The existing `handleRefresh` body-mode handler must refresh OIDC-issued sessions correctly. Two failure modes need explicit verification at PR-5b: (a) the local handler issues a fresh JWT from `session.CachedUser` without re-validating with the IdP — meaning IdP revocation (account disabled, group removed) takes up to 7 days to take effect, an unacceptable post-employment access window for a Kubernetes management plane; (b) the OIDC refresh token's IdP-bound metadata is silently discarded. PR-5b must either: (i) add an IdP-side `revocation_endpoint` re-check on every refresh for OIDC-sourced sessions (best); (ii) cap OIDC-sourced refresh token TTL to a shorter window (e.g., 1 hour rather than 7 days, forcing more frequent re-auth) and document this; or (iii) explicitly document in the privacy policy that IdP revocation may take up to 7 days to take effect, and add a "session lifetime: 7 days" disclosure on the OIDC consent surface. Shipping silent 7-day post-revocation access is not acceptable.
- **Per-provider OIDC mobile button styling.** PR-5c renders OIDC providers as generic "Sign in with {DisplayName}" buttons. Per-provider branding (Google logo, Microsoft logo, etc.) is a follow-up polish item.
- **Animated counter UX on the Metrics tab.** M4 deferred this from PR-4b; M5 does not pick it up. Static numeric labels stay.
- **Drawer go-then-pop fix** (PR-1d #22 carryover). Origin plan explicitly says "Re-evaluate only if real users report jank." M5 does not re-open without user signal.
- **ESO sync history surface.** The backend read endpoint still does not exist; M5 does not add it. Backend addendum + mobile work happens together post-M5 if real demand surfaces.
- **Cluster-wide LogQL WebSocket (`/ws/logs-search`).** M4 deferred per "wait for user signal". No signal yet; stays deferred.
- **Drift-only revert action (future backend feature).** The agent research confirmed the web does not have a "Drift Revert" button — the M4 mobile label was wrong. Force Sync is the actual existing action. If real operators ask for a separate "revert-only-the-drift-without-full-sync" action distinct from Force Sync, file a backend feature request before mobile work.
- **Saved Views, custom dashboards, phone-side exec terminal.** All remain post-mobile per `mobile-app.md`.
- **Apple Watch / Wear OS.** Out of scope per `mobile-app.md`.
- **Custom themes via `/v1/themes`.** Deferred to v2 per `mobile-app.md`.

---

## Context & Research

### Relevant Code and Patterns

- `mobile/lib/auth/auth_repository.dart:139` — `listProviders()` currently calls `.where((p) => p.isCredentialProvider)`, silently stripping OIDC providers (the filter is at the list-method, not `fromJson` at line 34). PR-5c lifts this filter (or adds a sibling `listOIDCProviders()` method) and adds an OIDC branch in the providers UI.
- `mobile/lib/features/login/login_screen.dart:115-150` — `providersAsync.when(data: ...)` block. PR-5c adds a second pass filtering `kind == 'oidc'` and renders "Sign in with {DisplayName}" buttons.
- `mobile/lib/api/auth_token_holder.dart:7-23` — in-memory token holder. No changes needed for OIDC — the OIDC path writes to the same `authTokenHolderProvider.set(accessToken)` + `secureTokenStoreProvider.writeRefreshToken(refreshToken)` as local login.
- `mobile/lib/api/dio_client.dart:126-236` — `AuthInterceptor` handles 401 refresh transparently. Same path for OIDC-issued tokens.
- `mobile/pubspec.yaml:49-50` — `flutter_custom_tabs: ^2.2.0` already declared with "In-app browser for OIDC (PR-1g)" comment. PR-5c is the consumer.
- `mobile/lib/features/settings/theme_picker_sheet.dart` — only file in the settings dir. PR-5a creates the full Settings screen.
- `mobile/lib/features/eso/eso_widgets.dart:360-389` — `DisabledRevertDriftButton` (class at line 360; current label text is "Revert drift"). PR-5e replaces it with an active "Force Sync" button (rename) that calls a new `EsoRepository.forceSync` method.
- `frontend/islands/ESOBulkRefreshDialog.tsx` — 3-phase modal: scope-load, confirm, submit+progress-poll (2s). PR-5e ports this as a mobile bottom sheet with an additional scope-pick phase (because the web dialog is launched from a per-store / per-namespace context but mobile consolidates entry into a single button on the ESO dashboard, so scope must be picked first).
- `mobile/lib/features/resources/secret_screens.dart` — `SecretDetailScreen` with `_revealed` state. PR-5d wires the `SecureScreenMixin` here.
- `mobile/lib/widgets/kube_line_chart.dart:235-296` — `LineChart(LineChartData(...))` wrapper, currently `StatelessWidget`. PR-5f converts to `StatefulWidget` with `_ZoomState`.
- `mobile/lib/widgets/resource_table.dart:119` — `DataTable2` with eager `rows: [for ...]`. PR-5i fixes to a virtualized source (either `DataTable2.fromDataSource(...)` for continuous scroll on the 6000-row vuln list or `PaginatedDataTable2(source: KubeDataTableSource(...))` for smaller paginated lists — decision per surface).
- `mobile/lib/theme/kube_theme_builder.dart:17-59` — `KubeColors` with 19 semantic tokens. PR-5h reads the generated hex from `themes.g.dart` and adds contrast tests.
- `mobile/lib/widgets/refreshable_controller.dart` — race-protection mixin. PR-5e and PR-5f both extend it for new controllers.
- `mobile/lib/widgets/confirm_sheet.dart` — type-to-confirm pattern. PR-5e re-uses this for Force Sync confirmation.
- `mobile/fastlane/Fastfile` — `beta_ios` and `beta_android` lanes. PR-5j adds `promote_ios` and `promote_android` lanes.
- `mobile/docs/RELEASE.md` — current release runbook (TestFlight + Play Internal). PR-5j extends with public-store promotion steps.
- `backend/internal/auth/oidc.go:109-204` — `LoginRedirect()` + `HandleCallback()`. PR-5b adds a sibling `HandleMobileExchange()` that accepts client-supplied PKCE verifier.
- `backend/internal/auth/oidcstate.go:16-77` — `OIDCFlowState` + `OIDCStateStore`. PR-5b does NOT use this store; mobile state is client-side (CSRF protection on the mobile side echoes state back, server only validates code+verifier).
- `backend/internal/server/handle_auth.go:93-158` — `handleRefresh()` body-mode pattern (cookie-or-body detection, response echo). PR-5b's new handler mirrors this exactly.
- `backend/internal/server/routes.go:51-60` — auth route block. PR-5b adds `POST /v1/auth/oidc/{providerID}/mobile-exchange` here.
- `frontend/lib/auth.ts:61-78` — web `handleOIDCCallback()`. PR-5c's mobile equivalent calls the new body-mode endpoint instead.

### Institutional Learnings

This repo has no `docs/solutions/` tree. Learnings live inside `plans/mobile-app-m*.md` "Institutional Learnings" and "Carried-Over Issues" sections. Key carryovers for M5:

- **Cluster-pin race protection at result arrival, not just request initiation** (`plans/mobile-app-m4-pr-sequence.md:92`). M5's Force Sync and Bulk Refresh controllers must use `RefreshableController` with `_clusterStillPinned(_PinPhase.postEmission)` re-check.
- **Drift tri-state coloring** (`plans/mobile-app-m4-pr-sequence.md:95`). `Unknown` is `KubeColors.textMuted`, never red. Any post-Force-Sync state surface must respect this.
- **`executeAction` + `confirm_sheet.dart` for all writes** (M2 codification). PR-5e routes Force Sync and Bulk Refresh through this pattern, not bespoke widgets.
- **Secret-data destruction defense** (`CLAUDE.md` mobile invariants). PR-5d's FLAG_SECURE work touches the same screen — invariant unchanged.
- **MATCH_PASSWORD rotation order is destructive if reversed** (`mobile/docs/RELEASE.md`). PR-5j's lane changes must not require a password rotation; if they do, document the order explicitly.
- **Play requires manual first AAB upload** (`mobile/docs/RELEASE.md`). PR-5j's `promote_android` lane is gated on this — runbook step documented before lane runs.
- **Universal Links 4-source-of-truth constraint** (`mobile/docs/RELEASE.md`). PR-5j's metadata + screenshots cannot include URLs that bypass Universal Link verification.
- **Apple Developer Program enrollment is 24–48 hour blocker** (`mobile/docs/RELEASE.md`). PR-5j gated on enrollment complete; non-blocking if enrollment is parallel-tracked from the start of M5.
- **Vault-namespace router collision lesson** (PR-3f). For any new OIDC error route in PR-5c, errors must disambiguate (e.g., "state mismatch" vs. "PKCE verifier mismatch" vs. "IdP rejected code").
- **No documented a11y learnings yet.** PR-5h captures fresh ones into `mobile/docs/A11Y.md` (new file) so M6+ benefits.
- **No documented Sentry learnings yet.** PR-5a captures PII scrub patterns into `mobile/docs/OBSERVABILITY.md` (new file).

### External References

- `flutter_custom_tabs` 2.2.x — https://pub.dev/packages/flutter_custom_tabs. Cross-platform in-app browser (Android Custom Tabs, iOS SFSafariViewController).
- `sentry_flutter` ^8.x — https://pub.dev/packages/sentry_flutter. Crash reporting, performance monitoring (we disable performance to avoid PII in HTTP breadcrumbs).
- `flutter_windowmanager` ^0.2.x — https://pub.dev/packages/flutter_windowmanager. Android-only; sets `FLAG_SECURE` on the platform window. iOS has no equivalent — use `AppLifecycleState` listener + a stacked blur widget.
- `package:crypto` (Dart SDK) — PKCE `code_challenge = base64url(sha256(code_verifier))` with `_=` stripping.
- WCAG 2.2 AA — https://www.w3.org/TR/WCAG22/. Specific success criteria for M5: 1.4.3 (Contrast Minimum 4.5:1 body / 3:1 large), 1.4.10 (Reflow), 1.4.11 (Non-text Contrast 3:1), 2.5.5 (Target Size 24×24 minimum, 44×44 enhanced), 2.4.6 (Headings and Labels).
- App Store Connect: https://developer.apple.com/app-store/review/guidelines/. Key sections for k8sCenter: 5.1.1 (Privacy), 5.1.2 (Data Collection and Storage), 5.4 (Push Notifications).
- Play Console policies: https://support.google.com/googleplay/android-developer/answer/9858738. Key sections: Data Safety section in store listing, sensitive permissions justification.
- `data_table_2` 2.5.x — https://pub.dev/packages/data_table_2. `PaginatedDataTable2` + `DataTableSource` for virtualized rows.
- `coreos/go-oidc` v3 + `golang.org/x/oauth2` — backend OIDC libraries (PR-5b inherits, no version change).

---

## Key Technical Decisions

- **OIDC mobile uses client-side PKCE + client-generated nonce, not the server-side `OIDCStateStore`.** Mobile generates `code_verifier` (43-128 char random), derives `code_challenge` (S256), generates `state` (CSRF nonce) and `nonce` (ID-token-replay nonce) as separate values, opens IdP authorization URL with `code_challenge` + `code_challenge_method=S256` + `state` + `nonce`. IdP redirects back via Universal Link to `https://<mobile.universalLinkDomain>/m/auth/callback?code=...&state=...`. Mobile validates `state` matches what it sent (CSRF check), then POSTs `{code, state, codeVerifier, nonce, providerID}` to the new backend endpoint. Backend exchanges code+verifier with IdP, validates the ID token's `nonce` claim against the submitted nonce, issues JWT pair, returns in body. **Why client-side PKCE+nonce:** the in-app browser is a black box (`flutter_custom_tabs`) — server-side state across an opaque-browser hop is fragile. Client-side PKCE + client-supplied nonce is the standard native OIDC pattern (RFC 8252).
- **New endpoint: `POST /v1/auth/oidc/{providerID}/mobile-exchange`.** Body: `{code, state, codeVerifier, nonce}`. Response: `{accessToken, refreshToken, expiresIn, user}`. Mirrors PR-0's `handleRefresh()` body-mode pattern. CSRF: `X-Requested-With: XMLHttpRequest` required (mobile interceptor already injects). Rate-limited per IP (5/min, shared with login bucket). Audit log: `audit.ActionLogin` with provider ID detail. State has no server-side validation (client-validated for CSRF). The nonce IS server-validated against the ID token's `nonce` claim — closes the ID-token-replay window that PKCE alone does not cover (PKCE protects the code exchange; nonce protects ID-token integrity post-exchange).
- **Redirect URI: `https://<mobile.universalLinkDomain>/m/auth/callback` — inside the existing `/m/*` AASA wildcard.** Zero Helm chart change required. The existing iOS AASA (`helm/kubecenter/templates/well-known.yaml`) already grants the app rights for the entire `/m/*` path tree; Android's `<intent-filter>` already uses `pathPrefix='/m/'`. Using `/m/auth/callback` as the redirect path inherits both verifications without modification. Custom URL schemes (`k8scenter://`) are vulnerable to claim hijack on both iOS and Android — any installed app can register the same scheme and intercept the authorization code (RFC 8252 §7.2). Universal Links / App Links are domain-bound and OS-verified. **The alternative — adding `/auth/callback` outside the `/m/*` wildcard — was considered and rejected:** it would require a coordinated Helm chart upgrade across every operator deployment before any OIDC mobile flow could work, an unacceptable launch dependency.
- **Force Sync renames the misnamed "Drift Revert" mobile button.** Web/backend terminology is "Force Sync". Existing endpoint: `POST /v1/externalsecrets/externalsecrets/{ns}/{name}/force-sync`. Mobile button label changes from "Revert Drift" to "Force Sync"; tooltip on disabled state (non-local cluster) explains "Force Sync is local-cluster only — use the desktop UI for remote clusters."
- **Bulk Refresh modal sheet is a single stateful widget** backed by a `StateNotifier` that walks phases scope-pick → scope-load → confirm → submit → poll. `showModalBottomSheet` with `isScrollControlled: true` for the height. Scope-pick is mobile-specific (web's `ESOBulkRefreshDialog.tsx` is launched from per-store / per-namespace entry points; mobile consolidates them into a single entry on the ESO dashboard). The picker shows three options: refresh-all-for-store (requires picking a namespaced SecretStore), refresh-all-for-clusterstore (requires picking a ClusterSecretStore), refresh-all-in-namespace (requires picking a namespace). The picker uses existing `named_resource_picker.dart` for store/namespace selection. Cancellation closes the sheet; in-flight POST is cancelled via `CancelToken`.
- **Sentry is initialised lazily.** `main.dart` reads the `sentry_opt_in` shared_preferences flag BEFORE `runApp`. If false, no `SentryFlutter.init` call. If true, `init` runs with `dsn` from a build-time `--dart-define=SENTRY_DSN=...` (compile-time inlined). **DSN is extractable from the shipped binary** (`strings`/`apktool` on the APK/IPA can recover it — this is an inherent property of `--dart-define`, not a leak). Compensating Sentry-side controls (configured at PR-5j time): (a) inbound-data filter rejects events whose `release` field doesn't match the published app version; (b) project-level rate limit caps events/hour to bound quota exhaustion if an attacker spams the DSN; (c) `release: "kubecenter-mobile@<semver>+<build>"` so legitimate events are tagged and synthetic ones are dropped. The `promote_*` Fastlane lanes enforce `--obfuscate --split-debug-info` in the release build — stack frames in shipped Sentry events are obfuscated, not source-path-leaking. The `beforeSend` callback drops events when `kReleaseMode == false` so debug-session frames never reach the shared Sentry project. PII scrub via `beforeSend` callback applies the layered policy detailed in U1 (PR-5a): unconditional `user.*` strip; wholesale request-body and breadcrumb-URL query-param strip; k8s-path-segment-scoped scrub for exception and breadcrumb messages (positional match against `/v1/...` path segments and `namespace=/name=` key-value pairs — NOT an unrestricted name regex); FCM token strip via `[A-Za-z0-9_-]{100,}`; stack-frame fields (`abs_path`, `filename`, `module`, `function`) preserved unscrubbed for debuggability. An earlier draft's broad regex `/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/` over 12-char tokens is explicitly rejected — false-positive rate destroys crash usefulness on URL paths and Dart symbols, false-negative rate leaks under-12-char resource names like `vault-token`.
- **`SecureScreenMixin` abstracts FLAG_SECURE + iOS blur.** Used by `SecretDetailScreen` initially. On `initState`, when `_revealed.isNotEmpty`, calls `FlutterWindowManager.addFlags(FLAG_SECURE)` (Android) and registers an `AppLifecycleState` listener (iOS, pushes an `Overlay` blur on `inactive`/`paused`, removes on `resumed`). On `dispose`, clears both. `setSensitive(bool)` method exposed to the widget so reveal/conceal toggles update the lock state mid-screen.
- **`KubeLineChart` becomes `StatefulWidget` with `_ZoomState`.** `_ZoomState` holds `minX/maxX/initialMinX/initialMaxX`. A **custom `ScaleGestureRecognizer`** wrapper requires `pointerCount >= 2` before claiming victory in the gesture arena — single-finger horizontal drags cleanly defer to the parent `TabBarView`'s tab-swipe recognizer. `onScaleStart` snapshots the current range; `onScaleUpdate` adjusts `minX/maxX` based on `details.scale` + `details.focalPointDelta.dx` (horizontal only — vertical pinch is no-op). Double-tap (single-finger) resets to initial. `InteractiveViewer` rejected — clips the chart at the border and breaks the legend layout. Generic `GestureDetector` without the two-finger gate is also rejected — it would cause tab-swipe / chart-zoom gesture conflicts inside `ResourceDetailScaffold`'s `TabBarView`.
- **`KubeDataTableSource` for `ResourceTable` virtualization.** Implements `DataTableSource.getRow(index)` lazily. Replaces the `rows: [for ...]` materialization. Validated against a 6000-row vulnerability list (the worst case).
- **Onboarding `onboarded_v1` flag** uses a dedicated `shared_preferences` key, NOT a secure-storage proxy. `shared_preferences` is cleared on app uninstall on both platforms (unlike iOS Keychain, which can survive reinstall). The flag is set when the user dismisses/completes the tour. On app startup, if the flag is absent AND no refresh token is stored, route to onboarding; if the flag is absent but a refresh token IS stored (iOS keychain survival case), set the flag without showing the tour (the user is implicitly an upgrade). Tests set the flag via a mocked `SharedPreferences.setMockInitialValues`.
- **A11y test infrastructure: `package:flutter_test` + `meetsGuideline`.** Each PR-5h test reads the 7 generated themes, instantiates the screen with each theme, runs `meetsGuideline(textContrastGuideline)`, `meetsGuideline(iOSTapTargetGuideline)`, `meetsGuideline(androidTapTargetGuideline)`. Failures point at the offending widget + theme. CI runs this in `flutter test` as a regression gate.
- **Performance baseline: `flutter run --profile` + DevTools timeline.** Recorded once at PR-5i for: cold start, dashboard scroll, 1000-row resource list scroll, 6000-row vulnerability list scroll, Metrics tab chart render. Numbers documented in `mobile/docs/PERFORMANCE.md`. Optimisations applied only where the profile shows >16ms frame budget violations.
- **`promote_ios` lane** uses `deliver` / `upload_to_app_store` with `submit_for_review: true`, `force: true` (skip manual review of generated metadata diffs), `precheck_include_in_app_purchases: false`, `automatic_release: false` (manual release after approval).
- **`promote_android` lane** uses `upload_to_play_store(track: 'production', skip_upload_apk: true, skip_upload_aab: false)`. Production rollout starts at 10% (`rollout: '0.1'`); manual graduation to 100% via Play Console.
- **App Privacy questionnaire — categories collected:**
  - Identifiers: device identifier (FCM token, mobile_push_devices.id)
  - Usage Data: app interactions (Sentry, only when opted in)
  - Diagnostics: crash data + performance data (Sentry, only when opted in)
  - Not Collected: Contact Info, Health & Fitness, Financial Info, Location, Sensitive Info, Contacts, User Content, Search History, Browsing History.
  All Sentry-collected data is "Not Linked to You" (PII scrub) and "Not Used for Tracking."
- **App Store Review Information note (PR-5j):** the submission's "App Review Information → Notes" field includes an explicit clarification: *"k8sCenter is a thin client for the user's self-hosted Kubernetes management server. Kubernetes cluster credentials are stored on the user's server (the k8sCenter backend they deploy), encrypted with AES-256-GCM. The mobile app never stores cluster credentials on-device — it only stores its own session JWT after the user signs in to their k8sCenter instance, and an FCM device token for push notifications. The app is not a standalone Kubernetes client and does not connect directly to Kubernetes API servers."* Pre-empts the most likely reviewer confusion under guideline 5.1.2 (Data Collection and Storage).
- **Privacy policy at `https://kubecenter.io/privacy`.** Hosted via the public docs site. PR-5j creates the page if it doesn't exist; content drafted from the Helm chart values' existing comments + a standard SaaS-app template. **Open question** for the implementer: confirm the canonical privacy URL at PR-5j time.
- **No remote-cluster ESO write parity.** Backend explicitly returns 501 for remote-cluster ESO writes (`backend/internal/externalsecrets/actions.go:66-74`). Mobile gates Force Sync and Bulk Refresh buttons on `activeClusterProvider == 'local'`. Remote-cluster operators see the explanatory tooltip.

---

## Open Questions

### Resolved During Planning

- **OIDC PKCE storage:** client-side on mobile (RFC 8252 native pattern). Resolved via Phase 1 research.
- **OIDC button rendering on login screen:** filter `kind == 'oidc'` from existing provider list, render under credential form. Resolved via `AuthProvider.fromJson` inspection.
- **Force Sync vs. Drift Revert:** Force Sync is the canonical web+backend term; mobile button is renamed. Resolved via web + backend grep.
- **Bulk Refresh phases and endpoint contract:** 4-phase (scope-pick → scope-load → confirm → submit + poll) over three scope variants (store / clusterstore / namespace) each with their own resolve and submit endpoints per `backend/internal/server/routes.go:649-665`. Resolved by reading the backend route definitions directly — the earlier "single bulk-refresh endpoint" assumption was wrong and is corrected throughout this plan. Implementation must read `backend/internal/externalsecrets/handler.go` for exact request/response shapes before writing wire models.
- **FLAG_SECURE scope:** SecretDetailScreen only (when `_revealed` non-empty). Resolved via plaintext-reveal surface audit.
- **Sentry default state:** off by default, opt-in via Settings. Resolved via R5 + privacy posture.
- **Sentry PII scrub list:** namespace, resource name, secret name, user identity, FCM token. Resolved via repo research §B audit.
- **A11y test infra:** `meetsGuideline(textContrastGuideline)` + `iOSTapTargetGuideline` + `androidTapTargetGuideline`. Resolved via Flutter test docs.
- **DataTable2 virtualization:** `DataTableSource` + `PaginatedDataTable2`. Resolved via data_table_2 docs + PR-5i discovery.
- **Onboarding internal-beta skip:** proxy on stored refresh token presence. Resolved via UX-first reasoning.
- **`promote_*` lane patterns:** standard fastlane `deliver` + `upload_to_play_store` with track promotion. Resolved via fastlane docs.

### Deferred to Implementation

- **Privacy policy URL.** PR-5j confirms whether `https://kubecenter.io/privacy` already exists; if not, creates the page (docs PR alongside) before submitting to stores. Final URL may differ.
- **App Store screenshots — exact surfaces to capture.** Likely candidates: dashboard, resource detail, wizard step, log tail, metrics tab. PR-5j picks 5 surfaces per platform after walking the app on a real device.
- **Sentry `beforeSend` regex precision.** PR-5a starts with a conservative scrub set; PR-5j audits against a real-world Sentry test event before submitting for review. If any unexpected PII slips through, PR-5j patches before public release.
- **`flutter_appauth` vs. manual PKCE final decision.** PR-5c starts with manual PKCE per Key Technical Decisions. If iOS app review surfaces a deep-link friction we didn't anticipate, fall back to `flutter_appauth` and re-test. The body-mode backend endpoint works either way.
- **Per-resource service-name autoderivation algorithm.** PR-5f walks the resource cache and matches by label selector. Exact ranking when multiple Services match (e.g., a Service with broader selectors vs. a narrowly-targeted one) is decided at impl time — likely "most-specific selector wins" or "show all matches with a picker."
- **Onboarding upgrade-skip migration tap-point.** PR-5g chooses between (a) checking `secureTokenStoreProvider.readRefreshToken()` at app startup, (b) an explicit `app_version_first_run` flag. The former requires startup ordering with auth init; the latter is cleaner. PR-5g picks at impl time.
- **`promote_android` rollout percentage.** 10% is the proposed default; PR-5j may adjust to 5% / 20% based on Play Console maturity at the time.
- **Apple Developer enrollment timing.** Must start 48h+ before PR-5j merges. If enrollment is parallel-tracked from PR-5a, this is a non-issue. If enrollment is gated on PR-5j confidence, it's a 2-day delay.
- **TestFlight External "Public Link" vs. "Group" gate.** PR-5j decides whether to use a public TestFlight link (anyone with the URL joins) or a fixed external group (manual approval per tester). Public link is faster to set up; external group is safer for staged rollout.

---

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### OIDC mobile flow

```
Mobile                          Backend                        IdP
  │
  │ tap "Sign in with Authelia"
  │ generate code_verifier (random 64 char)
  │ code_challenge = base64url(sha256(verifier))
  │ state  = random 32-char nonce  (CSRF defence)
  │ nonce  = random 32-char nonce  (ID-token-replay defence)
  │
  ├─ flutter_custom_tabs.launchUrl(
  │     https://<idp>/authorize?
  │       client_id=...&
  │       redirect_uri=https://<universalLinkDomain>/m/auth/callback&
  │       code_challenge=...&
  │       code_challenge_method=S256&
  │       state=...&
  │       nonce=...&
  │       scope=openid+profile+email
  │   )
  │                                                              │
  │                                            user authenticates │
  │                                                              │
  │ ← redirect via Universal Link / App Link
  │   https://<universalLinkDomain>/m/auth/callback?code=...&state=...
  │   (OS-verified domain ownership; system routes to k8sCenter app)
  │ (browser closes)
  │
  │ verify state matches local CSRF token
  │
  ├──── POST /v1/auth/oidc/{providerID}/mobile-exchange ────────►│
  │     X-Requested-With: XMLHttpRequest                         │
  │     body: {code, state, codeVerifier, nonce}                 │
  │                                                              │
  │                                ├── oauth2.Exchange(          │
  │                                │     code,                   │
  │                                │     VerifierOption(verifier) │
  │                                │   ) ─────────────────────►  │
  │                                │                             │
  │                                ◄── id_token + access_token ──┤
  │                                │                             │
  │                                ├─ verify id_token signature  │
  │                                ├─ verify id_token.nonce == submitted nonce
  │                                ├─ map claims → User           │
  │                                ├─ s.issueTokenPair(user)      │
  │                                ├─ s.AuditLogger.Log(login)    │
  │                                │                              │
  │  ◄── 200 {accessToken,refreshToken,expiresIn,user} ──────────│
  │                                                              │
  │ store accessToken in AuthTokenHolder
  │ store refreshToken in secureTokenStoreProvider
  │ route to dashboard
```

### ESO Bulk Refresh modal — 4-phase state machine over 3 endpoint variants

```
[scope-pick]                                          (mobile-only)
  │ user picks variant:
  │   (a) refresh-all-for-store        → pick (namespace, store_name)
  │   (b) refresh-all-for-clusterstore  → pick (clusterstore_name)
  │   (c) refresh-all-in-namespace      → pick (namespace)
  │
  ▼
[scope-load]
  │ GET the variant's refresh-scope endpoint:
  │   (a) /externalsecrets/stores/{ns}/{name}/refresh-scope
  │   (b) /externalsecrets/clusterstores/{name}/refresh-scope
  │   (c) /externalsecrets/refresh-namespace/{namespace}/refresh-scope
  │ → response: { externalSecrets: [...], count, breakdown }
  │
  ▼
[confirm]
  │ shows count + per-namespace breakdown
  │ user types "REFRESH" to confirm
  │
  ▼
[submit]
  │ POST the variant's submit endpoint:
  │   (a) /externalsecrets/stores/{ns}/{name}/refresh-all
  │   (b) /externalsecrets/clusterstores/{name}/refresh-all
  │   (c) /externalsecrets/refresh-namespace/{namespace}
  │ ├─ 202 → jobId
  │ └─ 409 active_job_exists → attach to existing jobId
  │
  ▼
[poll]
  │ GET /v1/externalsecrets/bulk-refresh-jobs/{jobId} every 2s
  │ ├─ pending|running → update progress UI
  │ ├─ succeeded → close sheet, show snackbar
  │ └─ failed → keep sheet, show error
  │
  ▼
[done]
```

The exact wire shapes (response keys for the scope endpoints, 202-vs-200 on submit) are read from `backend/internal/externalsecrets/handler.go` at PR-5e implementation time, not assumed.

### `SecureScreenMixin` lifecycle

```
SecretDetailScreen extends StatefulWidget with SecureScreenMixin
  │
  ├─ initState
  │   └─ setSensitive(_revealed.isNotEmpty)
  │       ├─ android: FlutterWindowManager.addFlags(FLAG_SECURE)
  │       └─ ios: register AppLifecycleObserver
  │             └─ on inactive/paused: push blur overlay
  │             └─ on resumed: remove blur overlay
  │
  ├─ on user reveals a key
  │   └─ setSensitive(true)
  │
  ├─ on user conceals all keys
  │   └─ setSensitive(false)
  │
  └─ dispose
      ├─ android: FlutterWindowManager.clearFlags(FLAG_SECURE)
      └─ ios: unregister observer + remove blur if present
```

---

## Implementation Units

### U1. PR-5a — Shared primitives: Settings screen + Sentry scaffold + SecureScreenMixin + a11y test helpers

**Goal:** Land foundations every later PR depends on — full Settings screen with Sentry opt-in toggle, lazy Sentry init, `SecureScreenMixin` abstraction, a11y test helpers, and `mobile/docs/OBSERVABILITY.md` documenting the PII scrub policy.

**Requirements:** R5 (Sentry), R4 (FLAG_SECURE abstraction), R6 (a11y infra), R10 (Settings as a launch surface).

**Dependencies:** None.

**Files:**
- Create: `mobile/lib/features/settings/settings_screen.dart` — full Settings screen reachable from `domain_navigation_drawer.dart`. Renders three sections in display order: (1) **Appearance** — `ListTile` opening the existing `theme_picker_sheet.dart`; (2) **Crash reporting** — `SwitchListTile` for the Sentry opt-in (subtitle "Send anonymous crash reports to help us fix bugs"); (3) **About** — version + build number row, "Privacy Policy" tile linking to `https://kubecenter.io/privacy`, "Rate this app" tile linking to the App Store / Play Store listing, "Notifications device token" tile showing the registered FCM device with a "Revoke" action that calls `DELETE /v1/notifications/devices/{id}` and re-registers. **No Security section in M5** — biometric authentication is not implemented (no `local_auth` dependency, no biometric setup UI); adding the section without the feature would be misleading.
- Create: `mobile/lib/features/settings/sentry_controller.dart` — `Notifier<bool>` over `shared_preferences` key `sentry_opt_in`. Exposes `setOptIn(bool)`. On toggle to true, calls `SentryFlutter.init`; on toggle to false, calls `Sentry.close()`.
- Create: `mobile/lib/observability/sentry_init.dart` — `initSentryIfOptedIn()` function called from `main.dart` BEFORE `runApp`. Reads `shared_preferences` synchronously, calls `SentryFlutter.init` with PII-scrubbing `beforeSend` if flag is true. DSN from `--dart-define=SENTRY_DSN`.
- Create: `mobile/lib/observability/pii_scrubber.dart` — `beforeSend` implementation. (a) Strips `user.username`, `user.email`, `user.ip_address` unconditionally (3 lines, no regex). (b) Strips request bodies and breadcrumb query parameters wholesale (Sentry's built-in `sendDefaultPii: false` covers most; the scrubber removes the residual). (c) For exception messages and breadcrumb messages, replaces only k8s-path-segment values (the token AFTER a known path key — `/v1/resources/secrets/<ns>/<name>`, `namespace=<ns>`, `name=<name>`) — not unrestricted regex over the whole message. Path segments inside `/v1/...` URLs are matched against a fixed positional pattern, NOT a generic name regex. (d) Strips FCM device tokens via a tight pattern (`[A-Za-z0-9_-]{100,}` is effectively unique to FCM and is safe to scrub globally). (e) Does NOT scrub stack frame `abs_path`, `filename`, `module`, or `function` fields — these contain Dart source paths, not runtime k8s data, and scrubbing them destroys crash debuggability.
- Create: `mobile/lib/widgets/secure_screen_mixin.dart` — `mixin SecureScreenMixin<T extends StatefulWidget> on State<T>` providing `setSensitive(bool)`. Wraps `FlutterWindowManager` calls (Android) + `WidgetsBindingObserver` + blur overlay (iOS). Idempotent — calling `setSensitive(true)` when already sensitive is a no-op. **Debug build override:** `setSensitive(true)` is a no-op when `kDebugMode == true` to keep screen-recording bug-reproduction workflows functional. Doc comment: "FLAG_SECURE is suppressed in debug builds; release builds enforce the secure flag." **Compatibility with Riverpod `ConsumerStatefulWidget`:** the mixin's `on State<T>` constraint accepts `ConsumerState<T>` since `ConsumerState` extends `State` — verified by static check in PR-5d wiring. Doc comment notes: "Mix into the State subclass directly (`class _Foo extends ConsumerState<...> with SecureScreenMixin<...>`); do NOT bind to `ConsumerState<T>` in the mixin constraint or non-Consumer screens lose reusability."
- Create: `mobile/test/observability/pii_scrubber_test.dart` — regex coverage: namespace names (12+ chars), secret names, user emails, IPs, FCM tokens, breadcrumb URLs.
- Create: `mobile/test/widgets/secure_screen_mixin_test.dart` — Android: `FLAG_SECURE` added on `setSensitive(true)`, cleared on dispose. iOS: blur overlay appears on `AppLifecycleState.inactive`, hidden on `.resumed`.
- Create: `mobile/test/a11y_helpers.dart` — shared test helper `expectMeetsAllGuidelines(WidgetTester, {textContrast: true, iOSTapTarget: true, androidTapTarget: true})` over `meetsGuideline`. Reused by every PR-5h test.
- Create: `mobile/docs/OBSERVABILITY.md` — Sentry usage, opt-in policy, PII scrub spec, how to add new instrumentation without leaking PII.
- Modify: `mobile/lib/main.dart` — `runApp(...)` wrapped in `await initSentryIfOptedIn(); runApp(...)`.
- Modify: `mobile/lib/routing/app_router.dart` — wire `/settings` route to `SettingsScreen`.
- Modify: `mobile/lib/widgets/domain_navigation_drawer.dart` — add Settings entry.
- Modify: `mobile/pubspec.yaml` — add `sentry_flutter: ^8.x` (semver range — the SDK is well-maintained, range is acceptable), `flutter_windowmanager: 0.2.0` (exact pin — community package with a small contributor base, exact pin prevents auto-upgrade surprises on a security-sensitive primitive). `url_launcher: ^6.x` if not already declared (used by the onboarding intro card's "How to set up your server" CTA and the Settings About section's external links).

**Approach:**
- `SecureScreenMixin` uses `WidgetsBindingObserver.didChangeAppLifecycleState` for the iOS path. Blur overlay is a `Stack` child with `BackdropFilter(filter: ImageFilter.blur(sigmaX: 30, sigmaY: 30))` (heavier than 24 — at 24 small-font Secret values may still leak character outlines in the app-switcher snapshot) over a theme-aware scrim: `Container(color: Theme.of(context).colorScheme.surface.withOpacity(0.7))`. The scrim is theme-aware so the cover doesn't look broken on light themes; the surface color renders correctly on all 7 themes without per-theme branching.
- `SentryFlutter.init` is conditional on Android API 21+ / iOS 11+ (`sentry_flutter` floors). `pubspec.yaml` minimums already meet this.
- PII scrubber accepts a `SentryEvent`, returns a sanitised `SentryEvent` (or null to drop the event entirely if it contains too much PII to scrub safely).
- `mobile/lib/observability/` is a new package directory mirroring the `mobile/lib/notifications/` pattern.

**Patterns to follow:**
- `mobile/lib/features/settings/theme_picker_sheet.dart` — sheet structure, ListTile composition.
- `mobile/lib/notifications/fcm_service.dart` — service-shaped controller pattern.
- `mobile/lib/widgets/refreshable_controller.dart` — mixin shape.

**Test scenarios:**
- Happy path: open Settings, toggle Sentry on, verify `Sentry.isEnabled` true; toggle off, verify `Sentry.close()` called.
- PII scrub: exception message `"Failed to fetch /v1/resources/secrets/kube-system-prod/vault-token: 404"` → URL path segments replaced positionally → `"Failed to fetch /v1/resources/secrets/<namespace>/<name>: 404"`. The scrub matches BY position-after-known-path-key, NOT by name-shape regex.
- PII scrub: event with secret name `my-vault-token` → replaced by `<secret>`.
- PII scrub: event with `user.email = "alice@corp.io"` → email stripped.
- Edge case: PII scrub called with already-sanitised event → no-op.
- SecureScreenMixin Android: `setSensitive(true)` adds FLAG_SECURE; `setSensitive(false)` clears; dispose clears.
- SecureScreenMixin iOS: on `AppLifecycleState.inactive`, blur overlay inserted; on `.resumed`, removed. Verify blur is NOT inserted when `sensitive == false`.
- A11y helper: a known-failing widget triggers a `TestFailure` from `expectMeetsAllGuidelines`; a known-passing widget passes.
- Sentry opt-out path: when flag is false, no `SentryFlutter.init` is called; the `Sentry` static API is no-op.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean (~12 new tests).
- `make check-themes` clean.
- Manual smoke: open Settings, toggle Sentry, trigger a `throw` in a screen, verify Sentry receives the event in opt-in mode, receives nothing in opt-out mode.

---

### U2. PR-5b — Backend OIDC body-mode mobile exchange endpoint

**Goal:** New `POST /v1/auth/oidc/{providerID}/mobile-exchange` handler that accepts client-supplied PKCE verifier + code + state, exchanges with the IdP, issues JWT pair in the response body. Audit-logged identically to the existing web callback.

**Requirements:** R1 (OIDC mobile), R11 (web/Dart isomorphism — backend mirrors PR-0 refresh body-mode pattern).

**Dependencies:** None.

**Files:**
- Create: `backend/internal/server/handle_oidc_mobile.go` — `handleOIDCMobileExchange(w, r)` handler. Reads `{code, state, codeVerifier, nonce}` from JSON body, looks up provider, calls `provider.ExchangeMobile(ctx, code, codeVerifier, nonce)`, issues JWT pair via `issueTokenPair`, returns `{accessToken, refreshToken, expiresIn, user}` in body. No cookies set.
- Create: `backend/internal/auth/oidc_mobile.go` — `(*OIDCProvider).ExchangeMobile(ctx, code, codeVerifier, expectedNonce) (*User, error)` mirroring `HandleCallback` but accepting a client-supplied verifier + client-supplied nonce instead of looking them up from the state store. Verifies ID token signature, validates `id_token.nonce == expectedNonce` (replay protection — closes the window PKCE alone does not cover), maps claims to user. State parameter is client-validated for CSRF and is NOT re-validated server-side (mobile clients carry their own CSRF defence; server-side state replay defence comes from the IdP's single-use code TTL).
- Modify: `backend/internal/server/routes.go` — register `ar.With(middleware.RateLimit(s.RateLimiter)).Post("/oidc/{providerID}/mobile-exchange", s.handleOIDCMobileExchange)` in the `/auth` route group.
- Modify: `backend/internal/server/response.go` — **`issueTokenPair` currently unconditionally calls `s.setRefreshCookie(w, refreshToken, ...)` (`response.go:88`).** This refactor is MANDATORY, not optional: add a `cookieMode bool` parameter (or a sibling `issueTokenPairBodyMode` function) so `handleOIDCMobileExchange` can suppress the cookie. The current `handleRefresh` body-mode handler works around this by echoing the refresh token in the body in addition to the unconditional cookie set — a workable but ugly pattern. Fixing this properly here also lets `handleRefresh` body-mode skip the wasted Set-Cookie header. Existing callers (`handleOIDCCallback`, `handleLogin`) keep cookie-mode and are unchanged.
- Create: `backend/internal/server/handle_oidc_mobile_test.go` — table-driven test covering: happy path, invalid code, invalid verifier (PKCE mismatch), expired code, unknown provider, malformed body, missing CSRF header (X-Requested-With).
- Create: `backend/internal/auth/oidc_mobile_test.go` — `ExchangeMobile` table-driven test covering: PKCE verifier success, PKCE verifier mismatch, ID token verification failure, claim mapping failure, allowed-domain failure.

**Approach:**
- The `oauth2.Config.Exchange(code, VerifierOption(verifier))` call is the same in both the web `HandleCallback` and the new `ExchangeMobile`. The difference is purely where `verifier` and `nonce` come from (server-side store vs. request body).
- ID token validation: call `p.verifier.Verify(ctx, rawIDToken)` (identical to `HandleCallback` at `oidc.go:165-166`) — this validates signature, `iss`, `aud`, and `exp` claims via `go-oidc`. **Do NOT manually decode the JWT** — bypassing `Verify()` is an audit-rejectable shortcut that opens cross-client token injection and expired-token replay. After `Verify` succeeds, extract the `nonce` claim from `IDToken.Claims(&claims)` and compare to `expectedNonce` from the body. Mismatch → return error `"oidc id token nonce mismatch"` (audited, sanitised — does not leak expected or received nonce). The audience claim check is implicit in `Verify` because `oidc.NewProvider` is configured with the client ID.
- CSRF defence: requires `X-Requested-With: XMLHttpRequest` header (the mobile interceptor already injects this). Same rate-limit bucket as login (5/min/IP).
- Audit: `s.AuditLogger.Log(r.Context(), s.newAuditEntry(r, user.Username, audit.ActionLogin, audit.ResultSuccess))` with detail `"oidc/<providerID>/mobile"`. Failure paths log `audit.ResultFailure` with detail explaining the failure mode (sanitised — no token, nonce, or verifier contents in the audit string).
- Response shape: `{"data": {"accessToken": "...", "refreshToken": "...", "expiresIn": 900, "user": {"username": "...", "displayName": "...", "groups": [...]}}}`. Mirrors `handleRefresh` body-mode shape plus a `user` payload (the mobile login screen needs username + display name on first auth).
- Refresh token rotation: the issued refresh token rotates on subsequent `/v1/auth/refresh` body-mode calls identically to local-issued tokens. No special handling.

**Patterns to follow:**
- `backend/internal/server/handle_auth.go:93-158` — `handleRefresh` body-mode shape and response echo.
- `backend/internal/server/handle_auth.go:210-293` — `handleOIDCCallback` token exchange + audit + user mapping.
- `backend/internal/auth/oidc.go:148-204` — `HandleCallback` token-exchange + ID-token-verify + claim-map flow.

**Test scenarios:**
- Happy path: valid `{code, state, codeVerifier, nonce, providerID}` → 200 with `{accessToken, refreshToken, expiresIn, user}` in body. No cookies set.
- Edge case: missing `codeVerifier` → 400 with body `{"error": {"code": 400, "message": "codeVerifier required"}}`.
- Edge case: missing `nonce` → 400 with body `{"error": {"code": 400, "message": "nonce required"}}`.
- Edge case: missing `code` → 400 with explanatory message.
- Edge case: unknown `providerID` → 404.
- Error path: IdP rejects the code (expired, replay, wrong verifier) → 401 with detail `"oidc exchange failed: ..."` (sanitised, no IdP-secret leakage).
- Error path: ID token signature verification failure → 401.
- Error path: **ID token nonce claim does not match submitted nonce** → 401 with detail `"oidc id token nonce mismatch"`. Audited as `ResultFailure` with the sanitised detail. This closes the ID-token-replay attack window.
- Error path: allowed-domain check fails → 403 with `email domain not allowed` detail.
- CSRF: missing `X-Requested-With` header → 403.
- Rate limit: 6th request in 60s from same IP → 429.
- Audit: success path logs `audit.ActionLogin` with detail `"oidc/authelia/mobile"`. Failure paths log `audit.ResultFailure` with reason.
- Integration: full token round-trip — exchange returns access token; subsequent `/v1/auth/refresh` body-mode with the returned refresh token rotates successfully.

**Verification:**
- `cd backend && go vet ./... && go test ./...` clean.
- Manual smoke against homelab Authelia: POST a mobile-exchange request with a valid code from a hand-crafted IdP flow; verify the response shape; verify the refresh token works against `/v1/auth/refresh`.

---

### U3. PR-5c — Mobile OIDC flow: PKCE client + `flutter_custom_tabs` + login screen seam

**Goal:** Operator with an OIDC provider taps "Sign in with {DisplayName}" on the login screen, in-app browser opens, user authenticates against the IdP, redirect returns to the app, mobile exchanges the code via the new backend endpoint and lands on the dashboard.

**Requirements:** R1 (OIDC mobile parity), R11 (web isomorphism), R12 (cluster-pin doesn't apply here — auth pre-cluster).

**Dependencies:** U2 (backend endpoint), U1 (Settings + observability scaffolding for any auth errors that get logged to Sentry).

**Files:**
- Create: `mobile/lib/auth/oidc_controller.dart` — `OIDCController` notifier handling PKCE+nonce+state generation, custom-tabs launch, Universal Link callback intercept, code exchange. State: `({String? providerID, String? state, String? codeVerifier, String? nonce, AsyncStatus status, Object? error})`.
- Create: `mobile/lib/auth/pkce.dart` — pure-Dart helpers: `generateCodeVerifier()` (64-char random), `codeChallengeFromVerifier(verifier)` (`base64url(sha256(verifier))` with `=` stripped), `generateState()` (32-char hex), `generateNonce()` (32-char hex). Uses `package:crypto`.
- Create: `mobile/lib/auth/oidc_repository.dart` — `OIDCRepository` class. Methods: `exchangeMobile({providerID, code, state, codeVerifier, nonce})` POSTs to `/v1/auth/oidc/{providerID}/mobile-exchange`. Returns the issued JWT pair + user payload.
- Create: `mobile/lib/auth/oidc_widgets.dart` — `OIDCProviderButton({provider, onTap})` widget. Used on the login screen.
- Modify: `mobile/lib/auth/auth_repository.dart:139` — the actual filter is `listProviders().where((p) => p.isCredentialProvider)`, NOT in `fromJson` at line 34. PR-5c either (a) drops the `.where` filter from `listProviders` and lets the login screen filter by `kind` itself, or (b) adds a sibling `listOIDCProviders()` method. `AuthProvider.fromJson` already accepts `kind: 'oidc'` — no change needed there.
- Modify: `mobile/lib/features/login/login_screen.dart:115-150` — second `providersAsync.when(data: ...)` pass: render OIDC `OIDCProviderButton`s under the credential form. Each button dispatches to `oidcControllerProvider.notifier.startFlow(providerID)`.
- Modify: `mobile/lib/routing/app_router.dart` — register a Universal Link handler for `/m/auth/callback?code=...&state=...`. On callback, invoke `oidcControllerProvider.notifier.completeFlow(code, state)`. Re-uses the existing Universal Link plumbing — same domain as notification deep-links.
- **No iOS/Android manifest changes.** `mobile/ios/Runner/Runner.entitlements` and `mobile/android/app/src/main/AndroidManifest.xml` already cover `/m/*` for Universal/App Links. The `/m/auth/callback` redirect URI inherits this coverage.
- **No Helm chart change.** The redirect URI `/m/auth/callback` lives inside the existing `/m/*` AASA + assetlinks.json wildcard (`helm/kubecenter/templates/well-known.yaml` line 63 + `mobile/android/app/src/main/AndroidManifest.xml` `pathPrefix='/m/'`). Both files are unchanged by PR-5c. The redirect URI is hardcoded in `oidc_controller.dart`'s authorization URL builder and registered identically with the IdP-client (operator-side configuration documented in `mobile/docs/RELEASE.md`).
- Create: `mobile/test/auth/pkce_test.dart` — `generateCodeVerifier` length + charset; `codeChallengeFromVerifier` known-value tests against RFC 7636 fixtures.
- Create: `mobile/test/auth/oidc_controller_test.dart` — happy path; state mismatch (CSRF); custom-tabs launch failure; exchange 401; provider not found.
- Create: `mobile/test/auth/oidc_repository_test.dart` — endpoint path; body shape; response parsing; error mapping.

**Approach:**
- PKCE verifier: 64-char `[A-Za-z0-9\-._~]` (RFC 7636 unreserved set), generated from `Random.secure()`.
- PKCE challenge: `base64url(sha256(verifier))` with trailing `=` removed.
- State (CSRF): 32-char hex from `Random.secure()`.
- Nonce (ID-token replay): 32-char hex from `Random.secure()`, distinct from state.
- Flow start: `OIDCController.startFlow(providerID)` generates verifier+challenge+state+nonce, **persists them to `flutter_secure_storage` under keys `oidc_pending_{verifier,state,nonce,providerID}` with a 5-minute TTL (epoch-stamped)**, calls `flutter_custom_tabs.launchUrl(authorizationUrl)` where `authorizationUrl` is built from the provider config exposed via `/v1/auth/providers`. **Why secure_storage, not in-memory:** Android can kill the app process while `flutter_custom_tabs` is in the foreground (low-memory, "Don't keep activities" developer mode, background reclaim). When the Universal Link redirect arrives, the app cold-starts, the Riverpod `ProviderContainer` is fresh, and `OIDCController` initializes with null state. Without persistence, the flow fails the CSRF check (state==null) for every cold-start re-entry — invisible on simulator/emulator (no memory pressure) and broken in production on older Android devices. Authorization parameters include `code_challenge`, `code_challenge_method=S256`, `state`, `nonce`, `redirect_uri=https://<universalLinkDomain>/m/auth/callback`, `response_type=code`, `scope=openid profile email`. **Implementation decision deferred** — whether mobile constructs the auth URL directly from `/v1/auth/providers` config or via a thin backend helper endpoint. PR-5c picks the shape with the fewest backend changes after re-reading `handleOIDCLogin` at impl time.
- Flow complete: `OIDCController.completeFlow(code, state)` reads `verifier/state/nonce/providerID` from `flutter_secure_storage`, validates persisted state matches the redirect's state (CSRF), calls `OIDCRepository.exchangeMobile(code, state, codeVerifier, nonce, providerID)`, on success writes tokens to `authTokenHolder` + `secureTokenStore`, **deletes all four `oidc_pending_*` keys**, routes to dashboard. On any failure path (CSRF mismatch, network error, exchange failure, TTL expired): delete the pending keys and surface the appropriate inline error.
- Error handling: distinct error states for `state mismatch` (CSRF), `custom-tabs launch failed` (no browser available), `consent denied` (IdP redirected back with `error=access_denied` — controller inspects the callback URL for `error=` parameter before extracting `code=`), `nonce mismatch` (backend rejected), `exchange 401` (IdP rejected), `network error`, `provider unknown`. Each routes to a different inline error on the login screen. **OIDC button states:** while the flow is in flight (status == loading) the OIDC button is disabled and renders a circular spinner inline (other OIDC buttons + the credential form are also disabled to prevent concurrent flows that would invalidate the PKCE/state/nonce); on success the login screen replaces with the dashboard; on error the spinner clears and the inline error renders below the button.
- **Redirect URI is the Universal Link / App Link domain, not a custom scheme.** Same domain as notification deep-links (`mobile.universalLinkDomain`). The OS verifies domain ownership via AASA (iOS) and assetlinks.json (Android). Custom URL schemes (`k8scenter://`) are not used because any other app on the device can register the same scheme and intercept the redirect (RFC 8252 §7.2).
- **Pre-condition for PR-5c merge:** Universal Link domain verification works end-to-end for the new `/auth/callback` path. If domain verification fails (AASA mis-served, assetlinks.json wrong fingerprint, or Helm chart not updated), the IdP redirect lands in a web browser instead of the app and the flow fails silently. Verification step is documented in `mobile/docs/RELEASE.md`.

**Patterns to follow:**
- `frontend/lib/auth.ts:61-78` — web `handleOIDCCallback` shape (mobile body-mode equivalent).
- `frontend/islands/AuthProviderButtons.tsx:57-72` — web provider button rendering.
- `mobile/lib/wizards/wizard_controller.dart` — controller race-protection inheritance (not strictly applicable here — auth pre-cluster — but pattern reused for the OIDC notifier).
- `mobile/lib/notifications/fcm_service.dart` — Universal Link / deep-link callback handler precedent on `app_router` (reused for the OIDC callback path).

**Test scenarios:**
- Happy path: tap OIDC button → custom-tabs launches → redirect completes → exchange returns 200 → dashboard.
- PKCE: verifier and challenge round-trip against RFC 7636 vector (Appendix B).
- CSRF: state in redirect doesn't match stored state → "Sign-in failed (security check)" inline error; no exchange POST.
- Edge case: custom-tabs not available (Android Custom Tabs disabled, or iOS < 11) → falls back to `url_launcher`; if that fails, surface an explanatory error.
- Error path: exchange returns 401 → "Sign-in rejected by identity provider" inline error.
- Error path: exchange returns 403 (domain not allowed) → "Your email domain isn't authorized" inline error with the domain.
- Error path: network error → "Couldn't reach the server. Check your connection and try again."
- Edge case: provider list contains only OIDC providers (no credential provider) → credential form is hidden; OIDC buttons stand alone.
- Edge case: provider list has multiple OIDC providers → each renders as a separate button with `DisplayName`.
- Integration: after OIDC login, `/v1/auth/refresh` body-mode rotates the refresh token successfully.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean.
- Manual smoke against homelab Authelia: log in on iOS Simulator + Android Emulator, verify the dashboard loads, verify refresh works after access-token expiry.

---

### U4. PR-5d — Secret screen FLAG_SECURE + iOS blur cover

**Goal:** Operator reveals a Secret key on `SecretDetailScreen`, then switches apps — the OS app-switcher snapshot shows a blurred placeholder (iOS) or is blocked entirely (Android), not the revealed plaintext.

**Requirements:** R4 (screenshot suppression).

**Dependencies:** U1 (`SecureScreenMixin`).

**Files:**
- Modify: `mobile/lib/features/resources/secret_screens.dart` — `SecretDetailScreen` extends `StatefulWidget` with `SecureScreenMixin`. On `_revealKey(key)` and `_concealKey(key)`, recompute `_revealed.isNotEmpty` and call `setSensitive(...)`.
- Modify: `mobile/lib/features/resources/secret_screens.dart` — replace direct `setState` reveal logic with a wrapper that updates the sensitive flag.
- Modify: `mobile/test/features/resources/secret_screens_test.dart` (if exists, else create) — verify `setSensitive(true)` is called on reveal and `setSensitive(false)` is called when all keys concealed.

**Approach:**
- The mixin already covers Android (FLAG_SECURE) and iOS (blur). The only screen-specific work is wiring the sensitive flag transitions.
- Conceal-all-on-route-pop: `dispose` already clears flags in the mixin — no extra work.
- When a Secret has multiple keys, sensitive stays true while ANY key is revealed.

**Patterns to follow:**
- `mobile/lib/widgets/secure_screen_mixin.dart` (created in U1).
- `mobile/lib/features/resources/secret_screens.dart` existing reveal/conceal toggle pattern.

**Test scenarios:**
- Happy path: open SecretDetailScreen → no FLAG_SECURE yet (nothing revealed) → reveal key 1 → FLAG_SECURE added → conceal key 1 → FLAG_SECURE cleared.
- Edge case: reveal key 1 → reveal key 2 → conceal key 1 → FLAG_SECURE stays added (key 2 still revealed).
- Edge case: route pop while a key is revealed → dispose clears FLAG_SECURE.
- iOS lifecycle: reveal key, send app to background → `AppLifecycleState.inactive` → blur overlay appears. Bring back to foreground → blur removed.
- iOS lifecycle: send app to background while nothing revealed → no blur overlay (the screen has nothing sensitive).
- Edge case: rapid reveal-conceal-reveal toggles → state stays consistent; no orphaned overlays.

**Verification:**
- `cd mobile && flutter test` clean.
- Manual smoke on iOS Simulator: reveal a Secret value, swipe up to app-switcher, verify blur. On Android Emulator: reveal, swipe to recents, verify the screen is blocked (Android shows a black or theme-colored placeholder per FLAG_SECURE).

---

### U5. PR-5e — ESO write parity: Force Sync + Bulk Refresh (local-cluster only)

**Goal:** Operator on the local cluster taps "Force Sync" on an ExternalSecret detail → confirms → drift cleared. Operator opens the ESO dashboard → taps "Bulk Refresh" → walks 4-phase modal (scope-pick → scope-load → confirm → submit+poll) → all ESs in the chosen scope refresh.

**Requirements:** R2 (Force Sync), R3 (Bulk Refresh), R11 (web isomorphism), R12 (cluster-pin discipline + local-only gate).

**Dependencies:** U1.

**Pre-implementation verification step:** Before writing any of the files below, the implementer reads `backend/internal/server/routes.go:632-665` and `backend/internal/externalsecrets/handler.go` (HandleForceSyncExternalSecret + HandleResolveStoreScope + HandleResolveClusterStoreScope + HandleResolveNamespaceScope + HandleBulkRefreshStore + HandleBulkRefreshClusterStore + HandleBulkRefreshNamespace + HandleGetBulkRefreshJob) to confirm the exact request/response shapes. Wire models in `mobile/lib/api/eso_repository.dart` are built from the actual handlers, not assumed from this plan's diagram. This is a CLAUDE.md Rule 10 (NO SEMANTIC SEARCH) pre-condition — endpoint contracts cannot be guessed.

**Files:**
- Modify: `mobile/lib/api/eso_repository.dart` — add `forceSync({namespace, name, clusterIdOverride})` calling `POST /v1/externalsecrets/externalsecrets/{ns}/{name}/force-sync`. Add three scope-specific resolve methods: `resolveStoreScope({namespace, name, clusterIdOverride})`, `resolveClusterStoreScope({name, clusterIdOverride})`, `resolveNamespaceScope({namespace, clusterIdOverride})`. Add three scope-specific submit methods: `bulkRefreshStore({namespace, name, clusterIdOverride})`, `bulkRefreshClusterStore({name, clusterIdOverride})`, `bulkRefreshNamespace({namespace, clusterIdOverride})`. Add `getBulkRefreshJob({jobId, clusterIdOverride})`.
- Modify: `mobile/lib/features/eso/eso_widgets.dart` — replace `DisabledRevertDriftButton` with `ForceSyncButton`. Gated on `activeClusterProvider == 'local'`; non-local renders the existing disabled state with new tooltip "Force Sync is local-cluster only — use the desktop UI for remote clusters."
- Modify: `mobile/lib/features/eso/eso_widgets.dart` — replace any existing disabled bulk-refresh placeholder with `BulkRefreshButton` that opens the new modal sheet.
- Create: `mobile/lib/features/eso/force_sync_controller.dart` — `RefreshableController`-extending notifier. `executeAction`-shaped write path. Pins cluster at construction, re-checks at result arrival.
- Create: `mobile/lib/features/eso/bulk_refresh_controller.dart` — `RefreshableController`-extending notifier. Walks the 4-phase state machine (scope-pick → scope-load → confirm → submit + poll). State holds a `BulkRefreshScope` sealed-class variant: `_Store(namespace, name)`, `_ClusterStore(name)`, `_Namespace(namespace)`. Each variant dispatches to the matching repository methods. 2s poll interval via a `Timer.periodic` cancelled in `ref.onDispose`. Handles 409 `active_job_exists` by attaching to the existing job.
- Create: `mobile/lib/features/eso/bulk_refresh_scope_picker.dart` — first-phase widget: 3-option toggle (Store / ClusterStore / Namespace) plus a `NamedResourcePicker` for the chosen variant's identifier. **Layering precondition:** before PR-5e, move `mobile/lib/wizards/widgets/named_resource_picker.dart` → `mobile/lib/widgets/named_resource_picker.dart` (small file move; reusable picker that no longer belongs in `wizards/`) so feature directories can import it without crossing the wizards layer boundary. Update existing wizard imports in the same move PR.
- Create: `mobile/lib/features/eso/bulk_refresh_sheet.dart` — modal sheet UI. `showModalBottomSheet(isScrollControlled: true)`. Phase-driven body composes `BulkRefreshScopePicker` (phase 1), scope-load progress (phase 2), confirm card with count (phase 3 — type-to-confirm "REFRESH"), poll progress UI (phase 4).
- Create: `mobile/test/features/eso/force_sync_controller_test.dart` — happy path; 501 on remote cluster path (controller asserts local); cluster-pin race (switch cluster mid-fetch → no commit); 4xx error mapping.
- Create: `mobile/test/features/eso/bulk_refresh_controller_test.dart` — full state machine walk; 409 active job attach; poll error handling; cancel mid-poll cancels `Timer`.
- Create: `mobile/test/features/eso/bulk_refresh_sheet_test.dart` — widget test pumping through all phases; type-to-confirm gate.

**Approach:**
- Force Sync confirmation re-uses `confirm_sheet.dart` (type-to-confirm with the ExternalSecret name as the input).
- Bulk Refresh confirmation re-uses the same `confirm_sheet.dart` pattern but with "REFRESH" as the magic word.
- 2s poll is bounded — after 5 min total elapsed without completion, surface "This is taking longer than expected" message and let the user dismiss (poll continues in the background until the sheet is closed or the job completes). **Poll-phase progress UI:** `LinearProgressIndicator` with `value` from poll response (`completed / total`) when the API exposes per-item progress; falls back to indeterminate (`value: null`) when it does not. A count-up label below reads "Refreshing N of M" sourced from the same poll response. After 5 min, the linear bar persists but a `Theme.of(context).colorScheme.tertiary`-colored "Taking longer than expected — feel free to dismiss" caption appears beside it. On success, the bar fills to 100% briefly, then the sheet dismisses and a snackbar confirms. On failure, the bar turns `KubeColors.error` and the sheet stays open with an explicit retry CTA. **Screen-reader announcements:** the poll phase wraps its progress label in `Semantics(liveRegion: true, ...)` so VoiceOver/TalkBack announces count updates without requiring focus changes.
- 409 attach: when submit returns 409 with `{"jobId": "..."}` in the body, immediately advance to the poll phase with that jobId. No re-confirm.
- Force Sync result UI: on 200, snackbar "Force Sync triggered for {namespace}/{name}". Backend returns immediately; actual drift clearing happens async. The controller calls `ref.invalidate(esoDetailProvider(...))` immediately after the snackbar, forcing an immediate re-fetch of the drift state — the chip transitions from `Drifted` → `Unknown` (in-flight) → `InSync` (after sync completes) without operator-visible UI inconsistency. The intermediate `Unknown` state renders as `KubeColors.textMuted` per the M4 drift tri-state invariant — never red.
- Drift tri-state coloring (`InSync`/`Drifted`/`Unknown` → success/warning/textMuted) respected in the post-Force-Sync state surface per PR-4h.

**Patterns to follow:**
- `frontend/islands/ESOBulkRefreshDialog.tsx` — 3-phase shape (mobile adds scope-pick → 4 phases), type-to-confirm, 409 attach.
- `frontend/lib/eso-api.ts` — force-sync + bulk-refresh wire format.
- `mobile/lib/widgets/confirm_sheet.dart` — type-to-confirm dialog.
- `mobile/lib/widgets/scale_sheet.dart` — modal bottom-sheet pattern for stateful flows.
- `mobile/lib/api/resource_actions.dart` — `executeAction` shape.
- `mobile/lib/wizards/wizard_controller.dart` — cluster-pin re-check at result arrival.

**Test scenarios:**
- Happy path: tap Force Sync on a local-cluster ES → confirm sheet → enter ES name → submit → 200 → snackbar.
- Edge case: tap Force Sync on a remote-cluster ES → button disabled; tooltip explains.
- Error path: 501 from backend → snackbar "Force Sync isn't available for this cluster" (defensive — UI should have blocked).
- Cluster-pin race: switch active cluster while confirm sheet is open → submit attempts → post-emission re-check fires → "request landed on pinned cluster — cancelled" inline.
- Bulk Refresh phase walk: open sheet → scope loads (cluster scope, 47 ESs across 12 namespaces) → confirm phase shows count + breakdown → type "REFRESH" → submit → poll → succeeded → snackbar "Bulk refresh complete (47 ExternalSecrets)".
- 409 attach: submit returns 409 + jobId → skip to poll phase, attach to existing job.
- Edge case: cancel during scope-load → CancelToken cancels Dio request; sheet closes cleanly.
- Edge case: cancel during poll → Timer cancelled; sheet closes; backend job continues server-side.
- Edge case: poll error mid-job (transient 503) → "Retrying..." inline; resumes on next tick.
- Edge case: 5+ minute job → "Taking longer than expected" indicator after 5 min; user can dismiss; sheet survives.
- Integration: post-Force-Sync, the ES detail screen's read controller re-fetches and shows updated `driftStatus`.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean.
- Smoke against homelab local cluster: Force Sync on a drifted ES; verify drift cleared in UI. Bulk Refresh across cluster scope; verify all ESs refreshed.

---

### U6. PR-5f — M4 polish carryovers: chart zoom, compliance time-range, admin all-NS LogQL, service-name autoderivation

**Goal:** Bundle four small carryovers from M4 into one PR. Each is a small surgical change to an existing M4 surface; together they close the M4 polish backlog.

**Requirements:** R9 (M4 polish bundle), R11 (web isomorphism — but no new web parity required; these are M5-only enhancements).

**Dependencies:** U1.

**Files:**
- Modify: `mobile/lib/widgets/kube_line_chart.dart` — convert `StatelessWidget` → `StatefulWidget`. Add `_ZoomState` with `minX/maxX/initialMinX/initialMaxX`. Add a `_TwoFingerScaleRecognizer` that overrides `acceptGesture` to require `pointerCount >= 2` before victory. Wrap chart in a `RawGestureDetector` using the custom recognizer for scale + a separate `DoubleTapGestureRecognizer` for reset. Single-finger horizontal drags defer to the parent `TabBarView`'s tab-swipe gesture cleanly.
- Modify: `mobile/lib/features/policy/compliance_history_screen.dart` (verified existing file containing `ComplianceHistoryScreen`) — add a `?days=N` preset picker above the history chart. Uses the existing `mobile/lib/widgets/time_range_picker.dart` widget (already a `SegmentedButton` — inherits its selected/disabled visual treatment); preset values `1 / 7 / 30 / 90` map to `?days=`. Loading state during refetch: chart fades to 50% opacity with a small spinner overlay; chips disabled until response arrives. Error state: chart shows last-known data with a "Couldn't refresh" inline banner + retry CTA; chips re-enabled. Custom-range date picker is deferred — the backend's compliance history endpoint accepts `?days=N` only; adding `?start=`/`?end=` is a backend addendum out of M5 scope.
- Modify: `mobile/lib/features/observability/logs/log_filter_bar.dart` — when user is admin (`authStateProvider.isAdmin`), render an "All namespaces" checkbox. When checked, namespace dropdown is disabled and the LogQL builder emits queries without the namespace label selector.
- Modify: `mobile/lib/features/mesh/golden_signals_tab.dart` — add a Pod/Deployment-resource entry point that derives the service name from the resource's labels via `resourceCacheProvider.findServicesForResource(...)`.
- Create: `mobile/lib/util/service_derivation.dart` — `findServicesForResource(resourceCache, namespace, labels) -> List<Service>`. Walks Services in the same namespace, filters by label selector match. Returns sorted by selector specificity (most-specific first).
- Modify: `mobile/lib/widgets/resource_detail_scaffold.dart` call sites for Pod + Deployment detail screens — wire a Golden Signals tab conditional on `findServicesForResource(...)` returning at least one match.
- Test: `mobile/test/widgets/kube_line_chart_zoom_test.dart` — pinch gesture adjusts range; double-tap resets.
- Test: `mobile/test/features/policy/compliance_time_range_test.dart` — picker selection re-fires controller fetch with new range.
- Test: `mobile/test/features/observability/logs/log_filter_bar_all_ns_test.dart` — admin user sees the checkbox; non-admin does not; checkbox toggles namespace selector disabled state.
- Test: `mobile/test/util/service_derivation_test.dart` — label-selector matching; specificity sorting; no-match case.

**Approach:**
- Chart zoom: `_ZoomState.scale = currentScale * details.scale.clamp(0.1, 10.0)`. `onScaleUpdate` adjusts `minX/maxX` symmetrically around the focal point. Horizontal pan only — vertical pinch is rejected (charts don't zoom vertically in M5). The two-finger gate is enforced at recognizer level so the parent `TabBarView` swipe wins on single-finger drags without ambiguity.
- Compliance time-range: backend's `/v1/policies/compliance/history` accepts `?days=N` (`backend/internal/policy/handler.go:317`). M5 ships fixed presets `1d / 7d / 30d / 90d`. Custom date ranges deferred — would require a backend addendum to switch to `?start=`/`?end=`, out of M5's "no new backend except OIDC" scope.
- Admin all-NS LogQL: backend's `/v1/logs/query` already accepts queries without a `namespace` label for admins (`enforceQueryNamespaces` gate). Mobile just needs to expose this on the UI.
- Service derivation: simple label-selector match. Specificity = number of labels in the selector that the resource has. Ties broken alphabetically. If the resource has no labels or no Services match, the Golden Signals tab is hidden.

**Patterns to follow:**
- `frontend/islands/PromQLQuery.tsx` — chart zoom inspiration (web doesn't have zoom either; mobile leads here).
- `frontend/islands/ComplianceHistoryChart.tsx` — time-range picker usage.
- `frontend/islands/LogFilterBar.tsx` — admin all-NS gate.
- `frontend/lib/service-derivation.ts` (if exists) — service derivation algorithm.
- `mobile/lib/widgets/refreshable_controller.dart` — controllers respect cluster-pin re-check.

**Test scenarios:**
- Chart zoom happy path: two-finger pinch out → range narrows; two-finger pinch in → range widens; double-tap → reset.
- Chart zoom critical case: **single-finger horizontal swipe on chart inside a `ResourceDetailScaffold` `TabBarView` → tab switches, chart does NOT zoom.** Verifies the two-finger gate works.
- Chart zoom edge case: pinch beyond initial range → clamped at initial.
- Compliance time-range happy path: select `7d` preset → re-fires fetch with `?days=7`.
- Compliance time-range edge case: select `90d` preset → re-fires fetch with `?days=90`. Custom date picker is NOT present (deferred per scope).
- LogQL all-NS happy path: admin user toggles checkbox → namespace dropdown disabled → submitting builds query without namespace selector.
- LogQL all-NS edge case: non-admin sees no checkbox.
- Service derivation happy path: Pod with labels `{app: web, tier: frontend}` → matches Service with selector `{app: web}` → Golden Signals tab visible.
- Service derivation edge case: Pod with no labels → no matches → tab hidden.
- Service derivation edge case: multiple matches → picker dropdown on the tab lets user choose which Service's signals to view.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean.
- Smoke against homelab: zoom on a metrics chart, verify smooth gesture handling. Toggle admin all-NS on a real cluster, verify cluster-scoped LogQL works.

---

### U7. PR-5g — First-launch onboarding tour

**Goal:** Public-store user installs the app for the first time, sees a 3-card swipeable tour explaining cluster pin / notifications / biometric unlock, then lands on the login screen. Internal-beta upgraders skip the tour silently.

**Requirements:** R8 (onboarding).

**Dependencies:** U1.

**Files:**
- Create: `mobile/lib/features/onboarding/onboarding_screen.dart` — `PageView` with 3 cards. Each card has an image, headline, body, and a Skip / Next CTA in distinct visual treatments (Skip = text/ghost button, Next/Get-started = primary filled button). Last card has "Get started" CTA that sets the `onboarded_v1` flag and navigates to login. Page indicator dots respect `semanticsLabel: 'Step N of 3'`.
- Create: `mobile/lib/features/onboarding/onboarding_cards.dart` — 3 card widgets: (1) `IntroCard` — "k8sCenter Mobile needs a home" — explains the self-hosted backend requirement; CTAs: "I have a server" (advances) + "How to set up your server" (opens `https://kubecenter.io/install` in the system browser via `url_launcher`); (2) `ClusterPinCard` — short orientation on the cluster picker; (3) `NotificationsCard` — "Enable Notifications" CTA fires the existing FCM permission flow, card advances regardless of outcome.
- Create: `mobile/lib/features/onboarding/onboarding_controller.dart` — `Notifier<bool>` over `shared_preferences` key `onboarded_v1`. Exposes `complete()` setting the flag. On app startup, reads the flag. If the flag is absent AND `secureTokenStoreProvider.readRefreshToken()` is non-null (iOS-keychain-survival edge case), silently set the flag without showing the tour. Otherwise, if the flag is absent, route to onboarding.
- Modify: `mobile/lib/routing/app_router.dart` — pre-login redirect: if `onboarded_v1 == false` AND `refreshToken == null`, route to `/onboarding`; otherwise route to login.
- Create: `mobile/test/features/onboarding/onboarding_controller_test.dart` — auto-skip when refresh token present; full-tour path; skip-mid-tour completion.
- Create: `mobile/test/features/onboarding/onboarding_screen_test.dart` — widget test pumping through all 3 cards.

**Approach:**
- Static cards. No video, no animation beyond `PageView` transitions. Each card is a `Column` with image + headline + body. Visual treatment: full-screen card with 24dp padding; image takes the top 40% of the card; headline (h2 weight, `KubeColors.textPrimary`) + body (`KubeColors.textSecondary`) below.
- Intro card targets public-store strangers who installed without a deployed backend. The "How to set up your server" CTA opens `https://kubecenter.io/install` in the system browser (NOT in-app — leaving the app to read setup docs is the intended behaviour). The "I have a server" CTA advances to card 2.
- Notifications opt-in card: when user taps "Enable Notifications", calls the existing FCM permission request flow (`mobile/lib/notifications/fcm_service.dart`). Result doesn't matter — the card advances regardless.
- The card images are simple iconography from built-in `Icons` (or a vetted icon pack if already declared in `pubspec.yaml`). Each `Icon` widget carries `semanticLabel` describing what the icon represents.
- **No biometric card.** Biometric authentication is not implemented in M5 (no `local_auth` dependency, no Settings → Security UI). Advertising it in onboarding without delivering it is a security misrepresentation under App Store guideline 2.3 (Accurate Metadata). When biometric auth ships in a future milestone, a new onboarding card can be added then.

**Patterns to follow:**
- `mobile/lib/features/settings/theme_picker_sheet.dart` — sheet/screen composition.
- `mobile/lib/notifications/fcm_service.dart` — permission request flow re-used.
- `mobile/lib/routing/app_router.dart` — pre-login redirect pattern.

**Test scenarios:**
- Happy path: fresh install, no refresh token, `onboarded_v1` false → app routes to `/onboarding` → swipe through 3 cards → "Get started" → flag set → login.
- Auto-skip: fresh install but `onboarded_v1` true (set by test fixture) → skips onboarding.
- Internal-beta upgrade: `onboarded_v1` false but refresh token present → skips onboarding (treated as upgrade).
- Skip-mid-tour: user taps Skip on card 1 → flag set → login.
- Notifications card: tap "Enable Notifications" → permission prompt fires → card advances regardless of outcome.
- Edge case: app killed during onboarding → on relaunch, restarts at card 1 (flag wasn't set).
- A11y: each card has `semanticsLabel` on the image; TalkBack reads headline + body in order.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean.
- Smoke on a fresh-install simulator: full 3-card walk; reinstall verifies the flag persists per device.

---

### U8. PR-5h — WCAG 2.2 AA accessibility pass across M1–M4 surfaces

**Goal:** Bring every M1–M4 surface to WCAG 2.2 AA. Add `Semantics` labels on status chips, icon buttons, pill indicators. Add contrast tests across 7 themes. Verify TalkBack/VoiceOver traversal order on the two most-used screens.

**Requirements:** R6.

**Dependencies:** U1 (a11y test helpers).

**Files (high-level — actual change spreads across ~40 screens):**
- Modify: every `mobile/lib/features/*/widgets/*.dart` and `mobile/lib/features/*/screens/*.dart` to add `semanticsLabel` parameters on icon-only buttons, status chips (e.g., `HealthyChip`, `DegradedChip`, drift state pills), and any `Icon(...)` used as a UI affordance without an adjacent text label.
- Modify: `mobile/lib/widgets/kube_theme_builder.dart` — add documented contrast guarantees in the doc comment of each `KubeColors` token (e.g., `/// textPrimary on bgBase: ≥4.5:1 across all 7 themes`).
- Modify: 1–2 themes in `shared/themes/*.json` IF the contrast audit identifies failing pairs. The theme generator regenerates `themes.g.dart` + `themes.generated.css` — `make check-themes` stays green.
- Create: `mobile/test/a11y/contrast_test.dart` — for each of the 7 themes, instantiates a "kitchen sink" widget (covers all `KubeColors` pairs in real combinations), runs `meetsGuideline(textContrastGuideline)`.
- Create: `mobile/test/a11y/tap_target_test.dart` — pumps every screen via a route walker, runs `meetsGuideline(iOSTapTargetGuideline)` + `meetsGuideline(androidTapTargetGuideline)`. May identify smaller icon-only buttons needing `minimumSize: Size(48, 48)`.
- Create: `mobile/test/a11y/login_traversal_test.dart` — TalkBack-equivalent traversal of `LoginScreen` produces expected order.
- Create: `mobile/test/a11y/resource_detail_traversal_test.dart` — same for `ResourceDetailScaffold`.
- Create: `mobile/docs/A11Y.md` — what was added, what to keep doing, manual TalkBack/VoiceOver smoke checklist.

**Approach:**
- This PR is wide. Realistic strategy: **(0) Discovery pre-pass** (single Haiku agent) enumerates all shared widget types used across multiple feature directories (`HealthyChip`, `DegradedChip`, drift state pills, generic icon buttons) and produces a canonical `semanticsLabel` per widget type. The shared widget definitions in `mobile/lib/widgets/` and `mobile/lib/widgets/empty_states.dart` get parameterized labels (or sensible defaults) — call sites do NOT add their own labels for shared widgets. **(1–5) Five parallel feature-directory agents** each handle ONLY the feature-local affordances (custom icons specific to one feature, screen-specific Semantics wrappers). The discovery pre-pass output is passed to each feature agent as a "do not duplicate-label these shared widgets" list. Each agent touches 5–8 files per CLAUDE.md Rule 5.
- The `Semantics(label: ...)` widget wraps any visual-only affordance. Use `MergeSemantics` to combine adjacent text + icon into a single accessibility node when they describe the same element.
- For dynamic content (e.g., a drift state pill that shows "InSync"), the label is computed from the state: `Semantics(label: 'Drift state: $stateName', child: ...)`.
- Tap target guideline failures are fixed by `IconButton(constraints: BoxConstraints(minWidth: 48, minHeight: 48), ...)` or by wrapping in `Padding(padding: EdgeInsets.all(12))` to extend the hit area.
- Dynamic type: verify `MediaQuery.of(context).textScaler` is respected by all text widgets (mostly automatic in Flutter; check for `style.copyWith(fontSize: 14)` hardcodes that bypass scaling).
- Sub-agent dispatch (parallel, Haiku-routable per CLAUDE.md model routing):
  - **Agent 0 (discovery pre-pass, sequential first):** scans `mobile/lib/widgets/` and `mobile/lib/features/**/widgets/` for widget types used across ≥2 feature directories. Outputs canonical semantics labels per shared widget. Modifies the shared widget definitions in-place to carry parameterized or defaulted labels.
  - **Agents 1–5 (parallel after Agent 0):** each handles only feature-local affordances using the discovery output as a "shared widget labels are already done" guard.
  - Agent 1: `mobile/lib/features/login/*`, `mobile/lib/features/dashboard/*`, `mobile/lib/features/resources/*` (~8 files)
  - Agent 2: `mobile/lib/features/observability/*` (metrics, logs, diagnostics — ~7 files)
  - Agent 3: `mobile/lib/features/eso/*`, `mobile/lib/features/certmanager/*` (~6 files)
  - Agent 4: `mobile/lib/features/gitops/*`, `mobile/lib/features/mesh/*` (~5 files)
  - Agent 5: `mobile/lib/features/policy/*`, `mobile/lib/features/scanning/*`, `mobile/lib/notifications_center/*` (~6 files)
  - Each agent returns: a diff + a list of widgets modified + any contrast/tap-target issues found. Cross-agent semantics-label divergence on shared widgets is impossible by construction because the shared widgets carry their labels themselves.

**Patterns to follow:**
- Flutter a11y docs: https://docs.flutter.dev/accessibility-and-localization/accessibility
- `package:flutter_test` `meetsGuideline` API.
- Existing `mobile/lib/widgets/secure_screen_mixin.dart` (U1) is one example of a state-aware widget.

**Test scenarios:**
- Per theme: `meetsGuideline(textContrastGuideline)` passes on a kitchen-sink widget with all `KubeColors` pairs.
- Per theme: `meetsGuideline(iOSTapTargetGuideline)` passes on every interactive element.
- Per theme: `meetsGuideline(androidTapTargetGuideline)` passes.
- Login screen: traversal order is `username field → password field → submit → OIDC button 1 → OIDC button 2`.
- Resource detail scaffold: traversal order is `back button → resource title → tabs (Overview, YAML, Events, optional extras) → tab content`.
- Status chip `Healthy` has `semanticsLabel: 'Resource is healthy'`; `Degraded` has `semanticsLabel: 'Resource is degraded'`.
- Icon-only refresh button has `tooltip: 'Refresh'` (which doubles as a semantics label).
- Dynamic type: `textScaler: 2.0` does not clip critical UI on iPhone SE simulator.
- Edge case: any reduced-motion preference (`MediaQuery.disableAnimations`) → page transitions reduce, animations pause.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean (all new a11y tests + no regressions).
- Manual TalkBack smoke on Android Emulator: login + browse + open resource detail. VoiceOver smoke on iOS Simulator: same.
- Documented in `mobile/docs/A11Y.md`.

---

### U9. PR-5i — Performance pass: DataTable2 virtualization fix + baseline measurement

**Goal:** Fix the known `DataTable2` eager-row materialization bug. Establish a performance baseline doc. Apply discovery-time optimisations where profiling shows >16ms frame-budget violations.

**Requirements:** R7.

**Dependencies:** U1.

**Files:**
- Modify: `mobile/lib/widgets/resource_table.dart` — replace `DataTable2(rows: [for ...])` with a virtualized variant over `KubeDataTableSource` (which implements `DataTableSource.getRow(int index)` lazily). **Per-surface choice:** continuous-scroll `DataTable2.fromDataSource(...)` for high-cardinality lists like vulnerabilities (no pagination control — scroll-virtualized only); `PaginatedDataTable2(source: ...)` for smaller lists where pagination is acceptable UX.
- Create: `mobile/lib/widgets/kube_data_table_source.dart` — `class KubeDataTableSource extends DataTableSource`. Holds the resource list, builds rows lazily. Listeners notified on data change.
- Modify: `mobile/lib/features/scanning/*` (if it currently uses `ResourceTable` for the 6000-row vuln list, otherwise verify it's already `ListView.builder`).
- Create: `mobile/docs/PERFORMANCE.md` — baseline measurements: cold start, dashboard scroll, 1000-row resource list, 6000-row vuln list, Metrics tab chart render. Each documented with the DevTools timeline numbers (worst-case frame ms over a 10s capture).
- Create: `mobile/test/widgets/kube_data_table_source_test.dart` — `getRow(0)` returns first row; `getRow(5999)` returns last row of a 6000-item source; `notifyListeners` on data update.
- Modify: `mobile/lib/widgets/resource_table_test.dart` (if exists) — update to expect `PaginatedDataTable2` shape.

**Approach:**
- `PaginatedDataTable2` with `rowsPerPage: 50` (page size) and `pageSyncApproach: PageSyncApproach.doNothing` (lazy by default). User scrolls within a page; "next page" button advances.
- For long lists (1000+), consider `data_table_2`'s `DataTable2.fromDataSource` constructor instead which is virtualized without pagination (continuous scroll). Decide at impl time which UX is better — probably continuous scroll for vuln lists, paginated for everything else.
- Profiling: `flutter run --profile` + DevTools timeline. Capture a 10s scroll session on each screen. Record worst-case frame ms.
- If the profile shows a screen consistently > 16ms, file a sub-task: identify the offending widget (`PaintingContext` or `MeasureContext` hotspot in the timeline), optimise (likely `const` constructors, `RepaintBoundary`, image caching).
- Cold start: `flutter run --profile` → time from process start to first frame. Record on both Pixel 6 emulator + iPhone 14 simulator equivalents.

**Patterns to follow:**
- `data_table_2` package docs.
- `frontend/islands/ResourceTable.tsx` — virtualization via TanStack Virtual (the web equivalent — mobile uses platform tooling).

**Test scenarios:**
- `KubeDataTableSource.getRow(0)` returns first row from a synthetic 6000-row source.
- `KubeDataTableSource.getRow(5999)` returns last row.
- Updating the underlying list calls `notifyListeners` and triggers a rebuild.
- Memory: pumping a 6000-row table with the source materializes only the visible rows (verifiable via `_KubeDataTableSourceState._rowCallCount` counter in test mode).
- Cold start: `flutter run --profile` captures ≤ 800ms time-to-first-frame on iPhone 14 simulator.
- Scroll: 6000-row vuln list maintains ≤ 16ms frame budget during fast scroll.
- Metrics tab: chart render ≤ 16ms with 5 series × 200 points.
- Edge case: source with 0 rows renders the empty state placeholder.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean.
- Manual `flutter run --profile` + DevTools timeline on a populated cluster: verify the documented baseline numbers.

---

### U10. PR-5j — Public-store launch: metadata, screenshots, app icons, `promote_*` lanes, privacy policy, final wire-up

**Goal:** Ship the public-store launch. App Store + Play Store production-track promotion lanes wired. Metadata + screenshots + icons in place. Privacy policy hosted. Apple Developer enrollment confirmed.

**Requirements:** R10 (public-store launch infra).

**Dependencies:** U1 (Settings + Sentry scaffold + SecureScreenMixin + a11y helpers) + U8 (a11y pass) + U9 (perf pass). **Crucially, NOT U2/U3 (OIDC), U4 (FLAG_SECURE), U5 (ESO writes), U6 (polish bundle), or U7 (onboarding).** If any of those slip, PR-5j can still ship the public-store launch on the origin's stated scope (a11y + perf + Sentry + store listing) and the feature work ships as a v1.1 patch update once it lands. This decouples launch timing from feature-completion timing and bounds the schedule risk of any single PR slipping. The feature PRs that DO complete before PR-5j submission are included in the launch build; ones that slip ship in the first patch release.

**Files:**
- Create: `mobile/fastlane/metadata/en-US/name.txt` — "k8sCenter".
- Create: `mobile/fastlane/metadata/en-US/subtitle.txt` — "Oncall companion for Kubernetes". (Matches the origin's "paged on the train" framing — sets accurate expectations for App Store discoverers; reduces 1-star-review risk from users expecting full management. "Kubernetes management on the go" was considered and rejected: it implies broad CRUD that M5 doesn't deliver, given only 2 write actions ship.)
- Create: `mobile/fastlane/metadata/en-US/description.txt` — ~3 paragraphs (App Store cap: 4000 chars).
- Create: `mobile/fastlane/metadata/en-US/keywords.txt` — comma-separated (App Store cap: 100 chars).
- Create: `mobile/fastlane/metadata/en-US/release_notes.txt` — M5 release notes.
- Create: `mobile/fastlane/metadata/en-US/privacy_url.txt` — `https://kubecenter.io/privacy`.
- Create: `mobile/fastlane/metadata/en-US/support_url.txt` — `https://github.com/<org>/k8scenter/issues`.
- Create: `mobile/fastlane/metadata/en-US/marketing_url.txt` — `https://kubecenter.io`.
- Create: `mobile/fastlane/metadata/en-US/age_rating.json` — "No Objectionable Content" (4+).
- Create: `mobile/fastlane/metadata/android/en-US/title.txt` — "k8sCenter".
- Create: `mobile/fastlane/metadata/android/en-US/short_description.txt` — ≤ 80 chars.
- Create: `mobile/fastlane/metadata/android/en-US/full_description.txt` — ≤ 4000 chars.
- Create: `mobile/fastlane/metadata/android/en-US/release_notes.txt`.
- Create: `mobile/fastlane/screenshots/en-US/` — phone + tablet, light + dark for both iOS (`iPhone 14 Pro Max`, `iPad Pro 12.9`) and Android (`phoneScreenshots/`, `sevenInchScreenshots/`, `tenInchScreenshots/`). 5 screenshots per platform/size/theme. Captured from real device runs.
- Create: `mobile/ios/Runner/Assets.xcassets/AppIcon.appiconset/Contents.json` + all icon sizes (iOS).
- Create: `mobile/android/app/src/main/res/mipmap-*/ic_launcher_foreground.xml` + background colour resources (Android adaptive icon).
- Create: `mobile/ios/Runner/Base.lproj/LaunchScreen.storyboard` (or update existing) — branded splash.
- Create: `mobile/android/app/src/main/res/drawable/splash.xml` (Android 12+ splash screen API).
- Modify: `mobile/fastlane/Fastfile` — add `promote_ios` lane (TestFlight → submit_for_review) and `promote_android` lane (Internal → Production 10% rollout).
- Modify: `mobile/fastlane/Appfile` — confirm bundle ID + team ID match the actual signing identity.
- Modify: `mobile/docs/RELEASE.md` — append public-store promotion runbook: prerequisites (Apple Developer enrollment, Play first-AAB manual upload), step-by-step for `promote_ios` and `promote_android`, troubleshooting.
- Create: `mobile/docs/APP_PRIVACY.md` — documented App Privacy questionnaire answers (Data Types collected, Data Use, Tracking). Includes a "What data this app does NOT store on-device" section explaining that Kubernetes cluster credentials remain server-side in the user's self-hosted k8sCenter instance (AES-256-GCM encrypted), never on the device. Mirrors the App Store Review Information note text exactly so reviewer and questionnaire tell the same story.
- Create or update: `frontend/routes/privacy.tsx` (or wherever the docs site privacy page lives — verify at impl time) — privacy policy content. Content sourced from a standard SaaS template + Sentry's own privacy commitments.

**Approach:**
- Phased Fastlane lane execution: `promote_ios` first (App Store review takes 24h–7d), then `promote_android` (Play production rollout). Both lanes log their actions to a release-notes channel (Slack/Discord webhook — TBD).
- Screenshots are captured manually from a real device or simulator (not automatic — fastlane snapshot tooling exists but adds complexity not worth M5 budget). Captured once at PR-5j time on a real homelab cluster.
- App icon design — use the existing k8sCenter web favicon as a base, render at iOS (1024×1024) + Android adaptive layers (foreground 432×432 layer + background colour). Designer-quality icon is a deferred polish; ship a workmanlike icon at M5.
- Privacy policy text: cover (a) what data is collected (FCM device token, opt-in Sentry crash data), (b) what data is NOT collected (everything else), (c) third-party services (Firebase Cloud Messaging, optionally Sentry), (d) user rights (right to delete via account deletion, right to access via support email), (e) contact email for privacy concerns.
- App Privacy questionnaire answers: see Key Technical Decisions section above. Categories: Identifiers (device ID), Diagnostics (Sentry, opt-in only), Usage Data (Sentry, opt-in only). All "Not Linked to You", "Not Used for Tracking".
- Rollout: Play production starts at 10%. Manual graduation to 100% via Play Console after 48h of clean crash/ANR data.

**Patterns to follow:**
- `mobile/fastlane/Fastfile` — existing `beta_ios` + `beta_android` lane structure.
- `mobile/docs/RELEASE.md` — existing internal-beta runbook tone.
- App Store Connect docs: https://developer.apple.com/documentation/appstoreconnectapi.
- Play Console docs: https://developers.google.com/android-publisher.

**Test scenarios:**
- Test expectation: this PR is largely metadata, screenshots, and lane definition. Functional tests are minimal. The verification is the actual store submission going through.
- `promote_ios` dry-run: `fastlane promote_ios --dry-run` validates metadata + screenshots + bundle ID without submitting.
- `promote_android` dry-run: `fastlane promote_android --dry-run` validates AAB + metadata + screenshots without uploading.
- Privacy URL: GET `https://kubecenter.io/privacy` returns 200 + a privacy policy page.
- App icon: opens correctly on home screen on iPhone 14 simulator + Pixel 6 emulator.
- Launch splash: shown for ≤ 1s during cold start; no flicker.
- App Privacy questionnaire dry-walkthrough: every category answered consistent with `mobile/docs/APP_PRIVACY.md`.
- Universal Link verification: tapping `https://<mobile.universalLinkDomain>/auth/callback?code=foo` from Safari (iOS) and Chrome (Android) opens the k8sCenter app to the OIDC completion handler (NOT the system browser). Confirms AASA + assetlinks.json are correctly served and OS-verified.

**Verification:**
- `fastlane promote_ios --dry-run` clean.
- `fastlane promote_android --dry-run` clean.
- Apple Developer enrollment confirmed in App Store Connect.
- Play Console first-AAB upload confirmed.
- Privacy policy page live.
- Submit `promote_ios` for actual review (separate from PR merge — runbook step). App Store review SLA: 24h typical, 7d worst case.
- Submit `promote_android` for actual review.
- Update `plans/mobile-app.md` and `plans/mobile-app-m5-pr-sequence.md` `status: active → completed` after both stores approve.

---

## Risk Analysis & Mitigation

| Risk | Mitigation |
|---|---|
| Apple App Store rejects the app on review (privacy, content, push notifications). | App Privacy questionnaire prepared in PR-5j; conservative Data Use answers; pre-review using App Store Connect's "Prepare for Submission" checklist. If rejected, the rejection feedback drives a follow-up PR. |
| Apple Developer Program enrollment delays public launch (24–48h lead time). | Start enrollment at PR-5a (parallel to all engineering work). Track in `mobile/docs/RELEASE.md` as a blocking external dependency. |
| Play first-AAB manual upload gate forgotten. | PR-5j runbook explicitly says: before `promote_android` runs, verify the manual upload has happened. The lane fails loudly if the API rejects. |
| MATCH_PASSWORD rotation accidentally destroys cert bundle. | Documented order in `mobile/docs/RELEASE.md`; PR-5j touches lanes but not match config. If rotation is needed, separate PR with explicit order checklist. |
| Mobile OIDC body-mode endpoint introduces an auth bypass. | Tight code review on PR-5b; CSRF posture matches existing `handleRefresh` body-mode; same rate-limit bucket; mandatory state validation on the mobile side (CSRF defense); audit log mirrors the existing OIDC callback. |
| Sentry leaks PII despite the scrubber. | PR-5a starts with a conservative regex set; PR-5j adds a real-world Sentry test event (synthetic crash with realistic k8s resource names) and inspects the event payload in Sentry before submitting for review. If unexpected PII slips through, patch in PR-5j before public release. |
| Sentry init costs cold-start time even when opt-out. | Sentry init is guarded by `if (optIn) await SentryFlutter.init(...)`. The off path is a single shared_preferences read. Measured in PR-5i baseline. |
| OIDC mobile flow breaks on Authelia / Keycloak / specific IdP. | PR-5c tests against homelab Authelia. Documented "compatibility tested with: Authelia 4.x" in PR description. Other IdPs surface as user reports post-launch. |
| OIDC authorization-code interception via redirect URI hijack. | Redirect URI is the existing Universal Link / App Link domain (`mobile.universalLinkDomain`), NOT a custom URL scheme. OS-verified domain ownership (AASA on iOS, assetlinks.json on Android) prevents other apps from claiming the same redirect target. PKCE plus server-side nonce validation provide defence-in-depth even if a redirect intercept somehow occurred. RFC 8252 §7.2 alignment. |
| `flutter_custom_tabs` 2.2 has a behavioural difference on iOS vs. Android. | PR-5c tests both. If divergence, the controller's error handling routes to platform-specific messages. |
| FLAG_SECURE conflicts with screen recording (legitimate debug session). | Documented in `mobile/docs/A11Y.md` and `mobile/docs/OBSERVABILITY.md` — users who need screen recording on a Secret screen must use a debug build. |
| iOS blur cover flickers on rapid app-switcher cycling. | Implementation tested with rapid swipe-up / swipe-down on Simulator. Idempotent `setSensitive` prevents orphaned overlays. |
| Bulk Refresh poll continues after sheet close, leaving an orphaned timer. | `Timer.periodic` is cancelled in `ref.onDispose`; controller's `_disposed` flag prevents post-dispose state writes. Test covers this. |
| A11y pass blows out scope — 40 screens × multiple themes × multiple guidelines is large. | Hard cap PR-5h at ~3 days of work. If contrast tests fail on a theme, fix in `shared/themes/*.json` (one-line edits). If a screen has dozens of semantics issues, file a follow-up PR rather than block M5. Document the deferred surfaces. |
| `DataTable2` virtualization fix breaks existing scroll behaviour on tablet. | PR-5i test pumps a synthetic 6000-row source; widget test ensures scroll behaves identically (or better). Manual smoke on a real tablet. |
| Performance baseline reveals optimisations needed beyond DataTable2. | PR-5i documents only — additional optimisations are out-of-scope follow-ups unless the regression is severe (e.g., > 100ms frame on a critical surface). |
| Onboarding upgrade-skip incorrectly fires for fresh installs (false positive). | The `refreshToken` proxy is checked AFTER `secureStorage.read` returns — if the keychain is empty (fresh install), the value is null and onboarding runs. Test fixture covers both paths. |
| Public-store-launch screenshots leak homelab cluster names or other identifiable data. | All screenshots reviewed manually before commit. If a cluster name slips through, regenerate the screenshot from a renamed cluster. |
| GitHub Actions CI bill or rate-limit during M5 (lots of TestFlight uploads). | PR-5j respects the existing CI cost budget; promotion lanes are manual-trigger only (not on every push). Internal-beta `beta_*` lanes continue running on `main` merges as before. |

---

## System-Wide Impact

- **Login screen** gains OIDC buttons; the credential form remains the default for local/LDAP users.
- **Settings screen** is new — appears in the navigation drawer for the first time. Sentry opt-in toggle + theme picker entry + About section.
- **`SecretDetailScreen`** screenshot-suppression behaviour is invisible until a key is revealed. Power users may notice the OS app-switcher snapshot becomes blurred/blocked.
- **ESO ExternalSecret detail** screens gain a "Force Sync" button (local cluster only).
- **ESO dashboard** gains a "Bulk Refresh" entry (local cluster only).
- **Metrics tab** (any kind with metrics) gains pinch-to-zoom.
- **Compliance history surface** gains a time-range picker.
- **LogQL editor** gains an admin-only "All namespaces" checkbox.
- **Pod / Deployment detail** gain a Golden Signals tab when a matching Service exists.
- **Resource list (tablet)** uses lazy row materialization — visually identical, perf-improved.
- **First-launch experience** changes — strangers see the 3-card tour; upgraders skip.
- **A11y surface area** improves across every M1–M4 screen — TalkBack/VoiceOver newly functional in the affected places.
- **Public store apps live** — k8sCenter is installable from App Store + Play Store. Internal-beta TestFlight + Play Internal continue in parallel.

**External contract surfaces touched:**
- New backend route `POST /v1/auth/oidc/{providerID}/mobile-exchange` (PR-5b). Mirrors existing OIDC flow's CSRF posture and rate-limit bucket.
- Existing Universal Link / App Link domain (`mobile.universalLinkDomain`) gains a `/auth/callback` path pattern (AASA + assetlinks.json + Helm chart updates). No new custom URL scheme — the OIDC redirect shares the same domain-verified mechanism as notification deep-links, eliminating the custom-scheme hijack class.
- New `pubspec.yaml` deps: `sentry_flutter`, `flutter_windowmanager`. Both are well-known maintained packages.
- New Fastlane lanes `promote_ios` + `promote_android` — invoked manually, not by CI.

---

## Documentation Plan

- `mobile/docs/OBSERVABILITY.md` (created PR-5a) — Sentry usage, opt-in policy, PII scrub spec, how to add new instrumentation safely.
- `mobile/docs/A11Y.md` (created PR-5h) — what was added, what to keep doing, manual TalkBack/VoiceOver smoke checklist.
- `mobile/docs/PERFORMANCE.md` (created PR-5i) — baseline numbers, profile capture process, frame-budget targets.
- `mobile/docs/APP_PRIVACY.md` (created PR-5j) — App Privacy questionnaire answers, Data Types collected, third-party services.
- `mobile/docs/RELEASE.md` (modified PR-5j) — public-store promotion runbook appended.
- `plans/mobile-app.md` (modified PR-5j merge) — `status: active → completed` flip. Roadmap item #9 closed.
- `plans/mobile-app-m5-pr-sequence.md` (modified PR-5j merge) — `status: active → completed` flip.
- `CLAUDE.md` Build Progress section (modified PR-5j merge) — append "Mobile M5 complete (public-store launch, OIDC, write parity, accessibility, perf, Sentry, onboarding)". Update Roadmap item #9 to closed.
- `README.md` (modified PR-5j) — App Store + Play Store badges + install links at top of the mobile section.

---

## Operational / Rollout Notes

1. **Apple Developer Program enrollment** — start at PR-5a kickoff. Track as a blocking external dependency in `mobile/docs/RELEASE.md`. Likely 24–48h after submission; longer if Apple requests entity verification (D-U-N-S, business documents).
2. **Play first-AAB manual upload** — confirm completed before PR-5j runs `promote_android`. Documented in existing `mobile/docs/RELEASE.md`.
3. **TestFlight External tester management** — PR-5j decides between Public Link (anyone with URL joins) and External Group (manual approval). Public Link is the default unless the team prefers controlled rollout.
4. **Play production rollout staging** — `promote_android` starts at 10%. Manual graduation in Play Console after 48h of clean crash/ANR data.
5. **App Store review SLA** — 24h typical, 7d worst case. PR-5j does NOT block PR merge on review approval; merge gates on dry-run lanes passing.
6. **Sentry DSN** — provisioned at PR-5a time. Build-time `--dart-define=SENTRY_DSN=...`. Stored in CI secrets (`SENTRY_DSN_MOBILE`). Documented in `mobile/docs/RELEASE.md`.
7. **Privacy policy publication** — PR-5j publishes the policy URL before submitting to stores. If the URL isn't live, App Store rejects with reason 5.1.1.
8. **Smoke against homelab** — PR-5a through PR-5i each smoke against homelab on the smoke pass (M4 convention). PR-5j adds a full end-to-end walkthrough on a real iOS device + Android device (not simulator).
9. **Rollback plan** — if a public-release build introduces a regression, halt rollout via Play Console (stop production rollout) and pull from sale via App Store Connect. Both are reversible within hours. Roll forward via patch release.
10. **CI workflow extension** — `mobile.yml` workflow keeps running `flutter analyze` + `flutter test` on every push. `promote_*` lanes are manual workflow_dispatch triggers, not push-triggered.

---

## Phased Delivery

- **Phase A (PR-5a):** Foundations. Settings + Sentry scaffold + SecureScreenMixin + a11y helpers. Mergeable on its own.
- **Phase B (PR-5b → PR-5c):** OIDC backend + mobile. Two PRs, dependency-ordered.
- **Phase C (PR-5d):** Secret screenshot suppression. Mergeable after Phase A.
- **Phase D (PR-5e):** ESO write parity. Mergeable after Phase A.
- **Phase E (PR-5f):** M4 polish bundle. Mergeable after Phase A.
- **Phase F (PR-5g):** Onboarding. Mergeable after Phase A.
- **Phase G (PR-5h):** Accessibility pass. Mergeable after Phase A; recommended after Phase B–F so the new surfaces are covered.
- **Phase H (PR-5i):** Performance pass. Mergeable after Phase A; recommended last among mobile-only phases so the baseline reflects all M5 features.
- **Phase I (PR-5j):** Public-store launch. Mergeable after Phase A + Phase G + Phase H + Apple Developer enrollment + Play first-AAB upload. **Phase I does NOT block on Phases B–F.** Whichever feature PRs are complete at PR-5j submission time are included in the launch build; the rest ship as v1.1 (App Store) / patch release (Play production track) once they land.

Phases B, C, D, E, F can run in parallel after Phase A and are non-blocking for the launch — they enrich it. Phase G (a11y) and Phase H (perf) are launch-blocking because the origin scope requires both. Phase I is the launch.

---

## Sources & References

- Master plan: [plans/mobile-app.md](mobile-app.md)
- M1 PR sequence: [plans/mobile-app-m1-pr-sequence.md](mobile-app-m1-pr-sequence.md)
- M2 PR sequence: [plans/mobile-app-m2-pr-sequence.md](mobile-app-m2-pr-sequence.md)
- M3 PR sequence: [plans/mobile-app-m3-pr-sequence.md](mobile-app-m3-pr-sequence.md)
- M4 PR sequence (most-recent pattern reference): [plans/mobile-app-m4-pr-sequence.md](mobile-app-m4-pr-sequence.md)
- Release runbook: [mobile/docs/RELEASE.md](../mobile/docs/RELEASE.md)
- Backend OIDC: `backend/internal/auth/oidc.go`, `backend/internal/auth/oidcstate.go`, `backend/internal/server/handle_auth.go:210-293`
- Backend refresh body-mode pattern (mirror for PR-5b): `backend/internal/server/handle_auth.go:93-158`
- Web OIDC: `frontend/lib/auth.ts:61-78`, `frontend/islands/AuthProviderButtons.tsx:57-72`
- Web ESO bulk refresh (mirror for PR-5e): `frontend/islands/ESOBulkRefreshDialog.tsx`
- Mobile login + auth: `mobile/lib/features/login/login_screen.dart`, `mobile/lib/auth/auth_repository.dart`
- Mobile ESO: `mobile/lib/features/eso/eso_widgets.dart:360-389`
- Mobile Secret: `mobile/lib/features/resources/secret_screens.dart`
- Mobile chart: `mobile/lib/widgets/kube_line_chart.dart:235-296`
- Mobile resource table: `mobile/lib/widgets/resource_table.dart:119`
- Fastlane: `mobile/fastlane/Fastfile`, `mobile/fastlane/Appfile`, `mobile/fastlane/Matchfile`
- Flutter a11y: https://docs.flutter.dev/accessibility-and-localization/accessibility
- WCAG 2.2 AA: https://www.w3.org/TR/WCAG22/
- `flutter_custom_tabs`: https://pub.dev/packages/flutter_custom_tabs
- `sentry_flutter`: https://pub.dev/packages/sentry_flutter
- `flutter_windowmanager`: https://pub.dev/packages/flutter_windowmanager
- `data_table_2`: https://pub.dev/packages/data_table_2
- RFC 7636 (PKCE): https://datatracker.ietf.org/doc/html/rfc7636
- RFC 8252 (Native OAuth Apps): https://datatracker.ietf.org/doc/html/rfc8252
- App Store Review Guidelines: https://developer.apple.com/app-store/review/guidelines/
- Play Console policies: https://support.google.com/googleplay/android-developer/answer/9858738
