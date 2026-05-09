# k8sCenter Mobile App — Master Plan

**Status:** active
**Date:** 2026-05-06

## Goal

Cross-platform Flutter app — iOS + Android, phones + tablets — reaching full parity with the k8sCenter web frontend across milestones M1–M5. Closes the "I got paged on the train" gap that the desktop-first web UI cannot serve.

## Architecture commitments

1. **Theme parity is enforced at build time.** `shared/themes/*.json` is the single source of truth. `tools/theme-gen/main.ts` emits `frontend/assets/themes.generated.css` (consumed by web) and `mobile/lib/theme/themes.g.dart` (consumed by mobile). `make check-themes` fails CI on drift.
2. **Backend changes are additive.** Cookie-based refresh stays the default for web; mobile uses a JSON-body fallback added in PR-0. The existing Slack/email/webhook channels are untouched; mobile push lands as a new `ChannelMobilePush` type alongside them.
3. **No offline cache layer.** App fails loud on disconnect with a clear retry path. Operators on flaky networks see real state, not stale.
4. **Tablet adaptive shell from day one.** Single 768px breakpoint via `LayoutBuilder` — no separate phone vs tablet codebase.

## Tech stack

Flutter 3.x stable, Dart 3.x, Riverpod 2.x w/ `riverpod_generator`, `go_router` + `go_router_builder`, `dio`, `web_socket_channel`, `flutter_secure_storage`, `firebase_messaging` + `flutter_local_notifications`, `xterm.dart` (tablet only), `code_text_field` + `highlight` (M2+), `fl_chart` (M4), Fastlane (`match` + `supply`) for signing & shipping.

## Milestones

- **M1 (4–6 wk)** — Foundation + read-only oncall companion. Login (local/OIDC/LDAP), cluster pill + bottom-sheet picker, namespace browse, resource list/detail (12 specialized kinds + generic for the rest), log tail, Notification Center feed, push delivery with deep-links, all 7 themes via the generator.
- **M2 (3 wk)** — Write actions + YAML editor. Scale/restart/rollback/suspend/trigger/delete on resource detail. ConfigMap/Secret YAML edit through `/v1/yaml/apply`. Type-to-confirm dialogs mirroring the web pattern.
- **M3 (5–7 wk)** — All 18 wizards. `WizardStepperMobile` mirrors `frontend/components/wizard/WizardStepper.tsx`. YAML preview step uses existing `/v1/wizards/:type/preview`.
- **M4 (4–6 wk)** — Observability + advanced surfaces. `fl_chart` over `/v1/monitoring/query_range`; LogQL editor + label browser; diagnostics blast-radius; full GitOps / mesh / cert-manager / ESO / policy / Trivy / Kubescape parity. **Out of scope:** topology graph — phone-sized rendering of a 2000-node-capped namespace graph is too much surface for too little oncall value; operators who need topology pull out a desktop.
- **M5 (2–3 wk)** — Polish + public store launch. WCAG 2.2 AA accessibility, perf pass, App Store + Play public listings, Sentry crash reporting (opt-in).

Internal-beta ships every `main` merge from end-of-M1 (TestFlight + Play Internal). M5 promotes to public stores.

## What landed in PR-0

PR-0 lays the foundation without yet producing a runnable Flutter app:

- **Theme generator pipeline.** `shared/themes/*.json` (7 files), `tools/theme-gen/main.ts`, `make check-themes` Makefile target, `frontend/assets/themes.generated.css` imported from `frontend/assets/styles.css`, placeholder `mobile/lib/theme/themes.g.dart`. `frontend/lib/themes.ts` carries a sync comment but is unchanged structurally.
- **Refresh body-token fallback.** `POST /v1/auth/refresh` reads the cookie first, falls back to JSON body `{"refreshToken":"…"}` when absent, and echoes the rotated token in the response **only in body mode**. Web behaviour byte-identical. New tests: `TestHandleRefresh_BodyMode`, `TestHandleRefresh_BodyMode_BadToken`, `TestHandleRefresh_CookieResponseHasNoRefreshToken`.
- **Mobile push channel.** `ChannelMobilePush` ChannelType + `sendMobilePush` dispatch arm + `mobile_push_devices` migration + `RegisterDevice/UnregisterDevice/ListDevicesForUser` store methods + `POST/DELETE/GET /v1/notifications/devices` endpoints + FCM HTTP v1 client (golang-jwt service-account flow, OAuth token cached, FCM credentials path read from `KUBECENTER_FCM_CREDENTIALS_PATH`; absent credentials => warn + no-op).
- **Mobile skeleton.** `mobile/README.md`, `mobile/.gitignore`. Flutter project itself begins in PR-1.

PR-0 is mergeable on its own — even if PRs 1+ never land, the body-token refresh and FCM channel are useful infra in their own right.

## What lands in PR-1+

PR-1 begins the Flutter project: `flutter create mobile/`, pubspec deps, theme/auth/api/cluster/routing scaffolding, login + dashboard + resource list, FCM device registration. CI workflow `.github/workflows/mobile.yml` and Fastlane lanes follow in PR-2.

## Open deferrals

- **Custom themes via `/v1/themes`** — runtime user-defined themes from the generator JSON pipeline. Defer to v2.
- **Offline write queue** — v1 fails loud. Revisit on operator demand.
- **Apple Watch / Wear OS** — out of scope.
- **Saved Views & Custom Dashboards** (previous roadmap #9) — still on the roadmap, post-mobile.
- **Phone-side exec terminal** — tablets only by default. Revisit on operator demand.
- **Secret-screen screenshot suppression** — `FLAG_SECURE` (Android) + iOS background-blur cover for `SecretDetailScreen`, so revealed plaintext doesn't appear in OS app-switcher snapshots or screen recordings. Surfaced by PR-1d code review (#17). Routed to **M5 polish** — fits naturally with the accessibility + perf pass.

Per-PR carryovers (smaller follow-ups tracked at the M1 plan level rather than here): see the **Carried-Over Issues** section in `plans/mobile-app-m1-pr-sequence.md`.
