# Changelog

All notable, operator-visible changes to k8sCenter are recorded here. This file
is intentionally GitOps-visible — Argo CD and Flux dashboards that surface a
repository's README also surface this changelog, so breaking changes and
upgrade notes reach operators without requiring a release-notes email.

Format roughly follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions are not pinned here — k8sCenter releases by Helm chart version. The
sections below group changes by phase / security-audit round so reviewers can
correlate findings back to the audit reports under `docs/security/`.

---

## Phase 6 — Scoping leaks (2026-05 audit)

Phase 6 closes four backend audit findings concerned with information leakage
across RBAC boundaries: P3-2 (CRD discovery + counts leak operator inventory),
P3-3 (diagnostics disclose pod/ReplicaSet details without pod RBAC), P3-4
(CRD update audit can record a different name than the object actually
updated), and P3-5 (Loki `/logs/labels` returns unscoped global label names).
The audit report is `plans/security-audit-2026-05-22.md`. All four findings
are backend Go work — no mobile, Helm, or web-frontend changes.

### Non-breaking changes

- **CRD inventory + counts filtered by per-user RBAC** (audit P3-2).
  `GET /api/v1/extensions/crds`, `GET /api/v1/extensions/crds/counts`, and
  `GET /api/v1/extensions/crds/{group}/{resource}` previously returned the
  full cluster-wide CRD inventory to any authenticated user, leaking the
  operator's installed-feature surface (External Secrets, cert-manager,
  GitOps, mesh, vulnerability scanners, etc.). Each endpoint now runs a
  cluster-wide `SelfSubjectAccessReview` for `verb=list` on every CRD's
  group+resource before including it in the response. The shared CRD
  discovery + count caches stay — counts are non-sensitive cluster
  aggregates — but the response is filtered per-request. Admins bypass the
  filter (matches the existing audit-log + secret-mask conventions). When
  the SSAR call itself errors, the entry is omitted and a warning is
  logged: fail-closed.

- **Diagnostics related-resource RBAC** (audit P3-3).
  `GET /api/v1/diagnostics/{ns}/{kind}/{name}` previously checked only the
  target kind's RBAC, then `Resolve()` listed pods and (for Deployments)
  ReplicaSets from the informer cache — leaking pod names, owner-reference
  chains, restart/failure states, and image-pull details to users who had
  RBAC on Deployments/Services but not Pods/ReplicaSets. The handler now
  precomputes a `RelatedRBAC{Pods, ReplicaSets}` struct via SSARs against
  the request's cluster context and passes it through `Resolve` →
  `resolveRelatedPods`. Denial is graceful — the related branch is
  skipped, downstream rules see an empty pod list, and the caller still
  receives the target-kind diagnostic findings. Lookup tables
  (`kindNeedsPods`, `kindNeedsReplicaSets`) drive which SSARs run for each
  target kind; pre-existing kinds without a pod traversal (PVC today) skip
  the pod check entirely.

- **CRD update URL/body identity mismatch rejected** (audit P3-4).
  `PUT /api/v1/extensions/resources/{group}/{resource}/{ns}/{name}`
  previously accepted any `metadata.name` / `metadata.namespace` in the
  request body, sent the body to the apiserver, and audited the URL name
  on success — letting a request URL name `foo` while the audit logged
  edits to `bar`, weakening forensics. A new pure helper
  `validateCRDUpdateIdentity` rejects body/URL mismatches with HTTP 400
  before any Kubernetes API call (empty body fields are still accepted —
  k8s tolerates omitting them on UPDATE). On success the audit entry now
  carries the apiserver-returned object's name + namespace rather than
  the URL values — the contract holds even if the mismatch guard is ever
  loosened.

- **Loki `/logs/labels` admin-gated** (audit P3-5).
  `GET /api/v1/logs/labels` previously called Loki's global `/loki/api/v1/labels`
  endpoint for any authenticated user. The pre-existing
  `buildNamespaceScopeQuery` plumbing was constructed but discarded
  unused — Loki's `/labels` endpoint only accepts a `query` parameter in
  2.4+ and even then doesn't reliably scope label *names* (the index may
  surface labels seen anywhere). Rather than rely on Loki version + query
  rewriting, the endpoint is now `middleware.RequireAdmin`-gated at the
  route layer. The scoped `/logs/labels/{name}/values` path stays
  available to non-admins because it already enforces a namespace-scoped
  LogQL selector via `enforceQueryNamespaces`. The dead scope-query code
  in `HandleLabels` was removed.

### Operator notes

- **Non-admin CRD inventory shrinks.** Users with namespace-only Role
  bindings (rather than cluster-wide ClusterRole bindings) will no longer
  see those CRDs in `GET /extensions/crds` — the SSAR for cluster-wide
  list returns Allowed=false. They can still list instances directly via
  `GET /extensions/resources/{group}/{resource}/{ns}` (the namespaced
  path enforces RBAC at the apiserver). If users report missing operators
  in the resource sidebar, verify their cluster-wide list permissions on
  the CRD's GVR (e.g., `kubectl auth can-i list <resource>.<group>`).

- **`/api/v1/logs/labels` now returns 403 for non-admins.** Operators
  using the UI's "label browser" surface should grant the user the
  `admin` role, or use `/logs/labels/{name}/values?namespace=<ns>` for
  the same data scoped to a single namespace.

- **Diagnostics may show fewer pod-derived findings for some users.** A
  user with Deployment-list but no Pod-list permission will see the
  Deployment-level diagnostic rules but no `PodImagePullBackOff`-style
  pod-derived results. The behavior matches existing platforms that
  silently omit RBAC-denied related resources.

- **Per-request SSAR cost.** P3-2 + P3-3 add per-request `SelfSubjectAccessReview`
  calls to the relevant endpoints. The existing `AccessChecker` 60s cache
  amortizes these — first request per user-per-minute pays the SAR cost,
  subsequent requests hit the cache. For clusters with very large CRD
  counts (200+), the first inventory load may take a noticeable beat;
  subsequent loads within 60s are sub-millisecond.

### Verification

- `go vet ./...` — clean repo-wide.
- `go test ./...` — all backend packages pass. New tests:
  - `internal/k8s/resources/crd_handler_p3_test.go` — 9 cases for
    `validateCRDUpdateIdentity` (empty fields, matching, name mismatch,
    namespace mismatch, fail-fast precedence, cluster-scoped resource
    handling); 8 cases for `splitGroupResourceKey` (normal, edge,
    malformed inputs).
  - `internal/diagnostics/rbac_p3_test.go` — 5 cases for `RelatedRBAC`
    allow/deny semantics (nil-permissive, zero-deny, partial, full),
    plus a lookup-table lock for `kindNeedsPods` / `kindNeedsReplicaSets`
    so regressions either over-broaden or under-cover detectably.
- `cd frontend && deno lint .` — clean repo-wide. (Pre-existing format
  drift in 504 frontend files and 40 type-check errors in
  `routes/api/[...path].ts` are not caused by Phase 6 — Phase 6 touches
  zero frontend files. These are deferred to a separate frontend
  housekeeping pass.)

### Known follow-ups (Phase 6 deferred)

- **Per-user CRD count cache scoping.** The shared count cache returns
  the same numbers to every user post-filter, even when the user has
  partial RBAC (e.g. list permission in some namespaces but not others).
  The audit's intent — "filter inventory" — is satisfied because users
  without cluster-wide list see nothing; users with cluster-wide list
  see correct totals. A future refinement would scope counts by the
  intersection of namespaces a user can list, but this would defeat
  caching for non-admins.
- **Diagnostics graceful-degradation visibility.** When pod/ReplicaSet
  RBAC denies the related-pod resolution, the diagnostic response shape
  is identical to "no failing pods found" — no signal to the operator
  that pod-derived rules were skipped. A future addition could surface
  a `relatedResourcesSkipped: ["pods", "replicasets"]` field so the UI
  can render a "view-only restricted" badge.
- **CRD update audit ResourceKind precision.** P3-4 fixed the
  ResourceName + ResourceNamespace fields but still audits the URL-derived
  `gvr.Resource` for `ResourceKind`. The apiserver could theoretically
  treat resource aliasing differently from what the URL suggests; future
  work could derive the audit kind from `updated.GroupVersionKind()`.
- **Loki label admin gate visibility on the mobile log-search screen.**
  Mobile's M4 LogQL editor calls `/logs/labels` for label autocomplete;
  non-admin mobile users will silently lose autocomplete after this PR.
  A future mobile follow-up could detect the 403 and show a hint that
  label autocomplete requires admin role.

### Out of scope for Phase 6

The audit's remaining findings are tracked for subsequent phases:

- **Phase 7** (supply chain + LDAP TLS): P2-11 govulncheck-clean Go
  toolchain + `golang.org/x/net` bump, P3-1 LDAP plaintext bind reject,
  P3-9 image digest pinning + gating Trivy.

Phase 4 deferred follow-ups (12 items) and Phase 5 deferred follow-ups
(13 items) remain open and tracked in earlier CHANGELOG sections.

---

## Phase 5 — Mobile UX hardening (2026-05 audit)

Phase 5 closes four mobile-side audit findings: P2-9 (FCM device
registrations not revoked on logout), P2-10 (Sentry preserves raw
request URLs), P3-6 (OIDC error callbacks clear pending PKCE before
validating state), and P3-7 (refresh-token secure-storage options
less explicit than pending-OIDC storage). The audit report is
`plans/security-audit-2026-05-22.md`. All four findings are
mobile-app-only — no backend, Helm, or web-frontend changes.

### Non-breaking changes

- **FCM device revoke on logout** (audit P2-9). `AuthRepository.logout`
  now calls `FcmRegistration.revokeCurrentDevice` BEFORE clearing the
  access token. The revoke flow resolves the current FCM token via
  `FirebaseMessaging.instance.getToken`, looks up the matching device
  record via `GET /api/v1/notifications/devices`, and issues
  `DELETE /api/v1/notifications/devices/{id}`. Then disposes the
  token-refresh and opened-app listeners. Best-effort throughout —
  Firebase-init failure, network blip, 4xx all swallowed so a flaky
  push backend can't strand the user signed in. Closes the gap where a
  signed-out device kept receiving notifications for the prior account.

- **Sentry `SentryRequest.url` scrub** (audit P2-10). `scrubEvent`
  previously copied `event.request.url` unchanged while nulling the
  sibling `queryString`, `data`, and `cookies` fields, so the audit-
  flagged leak of namespace/name path segments and any embedded
  `?token=…` query remained open. A shared `_scrubUrl` helper now
  slices `?query` and `#fragment` from the URL and runs the bare path
  through `_scrubText`; the same helper services HTTP breadcrumb
  `url` values so the two surfaces can't diverge. The sibling
  `SentryRequest.fragment` field is also nulled (it can carry the same
  identifiers as the URL fragment).

- **OIDC error-callback CSRF bind** (audit P3-6 part 1).
  `OIDCController.completeFlow` previously cleared pending PKCE+state
  on any callback with `error=…`, regardless of whether the callback's
  `state` matched the persisted pending. An attacker who could deliver
  a crafted error universal-link (Android intent spoofing, hostile
  webpage on iOS) could wipe the verifier/state of an in-flight
  legitimate flow — targeted login DoS. The error branch now reads
  `state` first and requires `callbackState == pending.state` to honor
  the error. Missing/mismatched state drops silently — same disposition
  as the existing "no pending" guard. Surfacing an error here would
  both confirm the crafted callback was received AND clobber whatever
  banner a previous legitimate failure had placed on the login screen.

- **Universal-link callbacks reject `http://`** (audit P3-6 part 2).
  `UniversalLinkListener._maybeDispatch` previously accepted both
  `https` and `http`. App Links / Universal Links rely on the
  OS-verified domain binding that AASA + assetlinks.json provide,
  which is a property of the https scheme only; honoring http opened a
  downgrade vector where an attacker who could serve content on
  `http://<host>/m/auth/callback` could intercept the redirect. Now
  scheme-restricted to https.

- **Refresh-token secure-storage explicit options** (audit P3-7).
  `FlutterSecureTokenStore` now mirrors the explicit-options pattern
  `FlutterSecurePendingOidcStore` already uses:
  - iOS: `KeychainAccessibility.first_unlock_this_device` — readable
    after first device unlock, not synced to iCloud, not restored from
    backups. The refresh token is a per-device credential; cross-device
    sync would defeat session-scoped revocation on the issuing device.
  - Android: `AndroidOptions(encryptedSharedPreferences: true)` —
    Jetpack EncryptedSharedPreferences (AES-GCM-256, key material in
    the Android Keystore) instead of the default per-value AES wrapper.

  Forward migration: a default-options legacy backend is held alongside
  the current one. On first post-upgrade read, the legacy backend is
  consulted, the value is migrated forward, and the legacy entry is
  deleted. Best-effort — if the migration write fails, the legacy
  value is still returned so bootstrap can refresh, and the legacy
  entry stays put for the next try. `deleteRefreshToken` clears both
  backends so a logout that happens before the first post-upgrade read
  can't leave a stale legacy entry that re-migrates on next launch.

### Operator notes

- **No re-login required on upgrade.** P3-7's forward migration is
  transparent: the first post-upgrade `readRefreshToken` finds the
  legacy entry, writes it under the new options, and deletes the
  legacy. Users stay signed-in across the version bump.

- **Push notifications stop on logout.** P2-9's silent revoke means
  users will no longer receive push for the previous account after
  signing out (previously they continued until the FCM token rotated,
  the device was unregistered manually, or the backend GC swept the
  row). No settings tile — silent revoke matches the "user signed out,
  push stops" mental model. The deferred-from-PR-5a settings tile is
  no longer needed; the stale comment in `settings_screen.dart` was
  removed.

- **Sentry projects only see scrubbed URLs going forward.** If the
  operator has been opted-in and the Sentry project contains historical
  events with raw URLs in `event.request.url`, those existing events
  are not retroactively scrubbed (Sentry-side data retention applies).
  Future events will arrive with paths scrubbed and query/fragment
  removed.

### Verification

- `flutter test` — all mobile tests pass after the Phase 5 work AND
  the review-fix commits below. New + extended tests:
  - `test/notifications/fcm_revoke_test.dart` — 6 tests covering
    happy path, no-match, empty list, GET failure, DELETE failure,
    malformed response.
  - `test/observability/pii_scrubber_test.dart` — 4 new tests for the
    initial scrub (query / fragment / non-k8s passthrough / null URL)
    plus 4 review-driven regression tests for the percent-encoded
    delimiter bypass (`%3F`, `%23`, breadcrumb parity, malformed
    encoding fallback). One existing test extended.
  - `test/auth/oidc_controller_test.dart` — 2 new tests for no-state
    and wrong-state error-callback drops; existing "consent denied"
    test extended to carry `&state=`; 5 review-driven tests covering
    `login_required`, `interaction_required`, `temporarily_unavailable`,
    `server_error`, and unknown error codes through the matched-state
    path.
  - `test/auth/universal_link_listener_test.dart` — 1 new test for
    http-scheme rejection.
  - `test/auth/secure_storage_test.dart` (new file) — 7 base tests
    covering current-hit, legacy-fallback-and-migrate, no-data,
    repeated reads, migration-write-failure preservation, write
    isolation, dual delete; plus 3 review-driven tests covering the
    Android BFU op-timeout fallback (current hang, both hang, write
    hang during migration).
- `dart analyze` — clean repo-wide across the mobile tree
  (`dart analyze` from `mobile/`, no scope filter).

### Applied during Phase 5 code review (one round of `/ce-code-review`)

The review surfaced several issues that were fixed inline before merge:

- **`fcm.dispose()` uncaught in logout** (reliability R-1, P1). The
  outer try/catch wrapped `revokeCurrentDevice` but not the immediately-
  following `dispose()`. `StreamSubscription.cancel()` returns a Future
  that can reject when the underlying stream has faulted (Firebase
  network drop, permission revocation mid-session); a rejection would
  propagate out of `logout()`, skipping `authTokenHolder.clear()` and
  `state = AuthUnauthenticated()` — the user would stay signed in with
  live credentials. Added a sibling try/catch.
- **`_scrubUrl` URL-encoded delimiter bypass** (adversarial ADV-3 +
  testing T-2, P2, cross-reviewer ×2). A URL like
  `https://kubecenter.local/v1/resources/secrets/ns/name%3Ftoken=leak`
  defeats `indexOf('?')`: the encoded `%3F` is literal text, not `?`,
  so the slice misses it and the segment regex stops at `%` — `token=leak`
  survives in the scrubbed output. Now percent-decodes via
  `Uri.decodeFull` BEFORE slicing. 4 regression tests added covering
  `%3F`, `%23`, breadcrumb parity, and malformed-encoding fallback.
- **Android BFU hang on migration** (learnings #1, P2). The new
  `readRefreshToken` migration sequence ran three sequential `await`s
  on FlutterSecureStorage with no timeout — Android EncryptedSharedPreferences
  can hang indefinitely in the Before-First-Unlock state (issue #270's
  earlier SharedPreferences fix established the `hydratePrefsWithTimeout`
  pattern). Now wraps each read in a 5s op-timeout; on timeout the call
  returns null and bootstrap falls through to the unauthenticated path —
  same disposition as "no stored refresh token". 3 timeout-branch tests
  added.
- **FCM revoke total-time bound** (reliability R-2, P2). The revoke
  flow issues two sequential Dio calls (`GET /devices` + `DELETE
  /devices/{id}`). Each call has a 30s `receiveTimeout` from
  `dio_client.dart`, so a hung backend could block logout for ~90s
  total. `revokeCurrentDevice` now bounds the inner `revokeDeviceByToken`
  call at 5s.
- **Device-ID leak in debugPrint** (reliability R-3, P2). The revoke
  list and delete error paths used `e.message` which Dio populates with
  the full request URL including the device ID. Now logs only the
  ApiError message or Dio error type name.
- **OIDC error-code coverage** (testing T-1, P2). The matched-state
  switch routes five distinct error codes (`access_denied`,
  `login_required`, `interaction_required`, `temporarily_unavailable`,
  `server_error`, wildcard) to four `OIDCFlowErrorReason` values. Only
  `access_denied` was tested through the matched-state path. Added 5
  tests so the switch can't drift undetected.
- **CHANGELOG verification language** (project-standards PS-2, P2).
  The verification line said "across all changed files" — accurate for
  the actual run command (`dart analyze` from `mobile/` with no scope
  filter, which IS repo-wide for the mobile tree) but the wording
  could read as scoped. Rewritten to be unambiguous.

### Known follow-ups (Phase 5 deferred — single review round)

This phase follows the Phase 3 and Phase 4 cadence: one round of
`/ce-code-review` plus this documented follow-up list rather than
chasing every regression through additional cycles. The deferred
items below are tracked here for the next audit:

- **Auth → Notifications import inversion** (maintainability M-1).
  `auth_repository.dart` now imports `notifications/fcm_registration.dart`.
  The domain-correct boundary is: auth emits a logout signal,
  notifications observes auth state and self-revokes (symmetric with
  the register-on-login `container.listen` in `main.dart`). The
  refactor requires designing a logout-event surface (an
  `AuthLoggingOut` intermediate state or a dedicated stream) — deferred
  to a follow-up because it's bigger than a review fix.
- **Logout-test wiring assertion** (testing T-3, maintainability T-1).
  The existing `logout: clears tokens` test in `auth_repository_test.dart`
  passes after Phase 5's change because `revokeCurrentDevice` is a no-op
  in the headless test environment (`!_supportedPlatform`). The
  order-of-operations contract — revoke fires while access token is
  still live, then clear — is not asserted. A spy-based test would
  close this gap; it lands naturally if M-1 (the inversion) is taken
  next.
- **revoke-during-ensureRegistered race** (correctness-1). If logout
  fires after `ensureRegistered` POSTs the device but before
  `_initialized = true`, the revoke is skipped and the backend row
  survives. Narrow window (Firebase token resolution latency); could
  be closed by setting `_initialized` immediately after the POST.
- **iOS Keychain accessibility-class persistence** (correctness-2).
  When the accessibility class changes between writes, the legacy
  Keychain item may persist with the old class rather than being
  replaced. Low confidence; needs real-device verification.
- **Stale rotated FCM device rows** (correctness-3 + adversarial ADV-1,
  cross-reviewer ×2, P2). Revoke deletes only the device row for the
  CURRENT FCM token. Rotated-and-orphaned device rows from prior
  registrations are left intact and could cross-bind if the same token
  is later assigned to another user. This is a pre-existing concern
  documented at `fcm_registration.dart:11`; resolution belongs in a
  backend old-token sweep PR.
- **`_scrubUrl` preserves user-info** (correctness-4). URLs like
  `https://user:pass@host/path` pass through with credentials intact.
  Mobile app never constructs such URLs, but Sentry breadcrumbs from
  third-party Dio interceptors could in theory. Worth a future
  defensive normalization.
- **OIDC error-callback silent-drop undebuggable** (reliability R-4).
  The silent-drop disposition prevents an attacker from confirming
  receipt of a crafted callback — but also blocks operator debugging
  of legitimate IdP misconfigurations that send unexpected state.
  Debug-build-only `debugPrint` or a scrubbed Sentry breadcrumb would
  preserve diagnostics without confirming receipt.
- **Migration delete-failure swallowed without log** (reliability R-5).
  When `_current.writeRefreshToken(legacy)` succeeds but
  `_legacy.deleteRefreshToken()` throws, the `catch (_)` block swallows
  silently. Session is safe; the stale legacy entry sits until next
  logout. A `debugPrint` in the catch would make persistent Keychain
  delete failures diagnosable.
- **`_FlutterBacked` premature abstraction** (maintainability M-2).
  Zero-behavior passthrough adapter introduced for type-coercion
  reasons. Could be inlined once migration completes and the legacy
  backend is removed.
- **`InMemoryTokenStore` missing `@visibleForTesting`** (maintainability
  M-3). Used by 100+ test files but lives in production lib/ without
  the annotation. One-line addition.
- **Migration partial-success test gap** (adversarial ADV-4). The
  `_WriteFailingStore` test covers write failure but not the symmetric
  delete failure after write succeeds. Mirrors reliability R-5's
  concern from a test-coverage angle.
- **fcm.dispose silences onTokenRefresh between sign-out and sign-in**
  (adversarial ADV-7). Pre-Phase-5, dispose only ran at container
  teardown. Now every logout cancels the subscription, so any FCM
  rotation between sign-out and the next sign-in is unobserved
  (re-synced on next `ensureRegistered`). Combined with ADV-1, extends
  the cross-user push leakage window slightly.
- **Sentry tags/extra/contexts structured-field audit** (learnings #5).
  `_scrubEventBody` covers `request`, `exceptions`, `breadcrumbs`, and
  `message`. The `tags`, `extra` map values, and `contexts` structured
  fields are not scrubbed. No known caller currently populates them
  with raw resource identifiers but a grep audit would confirm.

### Out of scope for Phase 5

The audit's remaining findings are tracked for subsequent phases:

- **Phase 6** (scoping leaks): P3-2 CRD inventory per-user RBAC, P3-3
  diagnostics related-resource RBAC, P3-4 CRD URL/body name mismatch,
  P3-5 Loki label scoping.
- **Phase 7** (supply chain + LDAP TLS): P2-11 govulncheck-clean Go
  toolchain + `golang.org/x/net` bump, P3-1 LDAP plaintext bind reject,
  P3-9 image digest pinning + gating Trivy.

Phase 4 deferred follow-ups (12 items documented in this changelog
above) remain open and tracked.

---

## Phase 4 — Network / SSRF / TLS hardening (2026-05 audit)

Phase 4 closes audit findings P2-6 (SSRF DNS rebinding window on
remote-cluster and monitoring URLs), P2-7 (backend NetworkPolicy
egress allowed all destinations on sensitive ports), P2-8 (chart
permitted plaintext exposure of authenticated app traffic), and P3-8
(dev PostgreSQL compose service bound to LAN with weak fixed
credentials). The audit report is `plans/security-audit-2026-05-22.md`.

### Breaking changes (operator action required)

- **TLS-by-default chart guard** (audit P2-8). The Helm chart now
  refuses to render any of three plaintext-exposed configurations
  unless the new top-level `security.insecureExposureAcknowledged`
  value is explicitly set to `true`:
  1. `ingress.enabled=true` with empty `ingress.tls`.
  2. `service.type=LoadBalancer` or `service.type=NodePort`.
  3. `backend.config.dev=true` combined with any externally-reachable
     service mode (LoadBalancer / NodePort / ingress-without-TLS).
  The acknowledgement is intended for fully internal trust-domain
  deployments where TLS termination happens upstream (managed LB with
  cert, service-mesh gateway) and the network path stays inside a
  trusted boundary. Default is `false` (safest posture). The bundled
  `values-homelab.yaml.example` opts in explicitly with a comment
  documenting the LAN-only trust boundary.

- **SSRF DNS resolution fails closed** (audit P2-6 part 1).
  `k8s.ValidateRemoteURL` previously allowed a connection through when
  DNS resolution failed; the rationale was that the k8s client would
  produce a more specific error. That left a window where transient
  NXDOMAIN, poisoned records, or rebinding flips between validation and
  dial could deliver requests to private endpoints the IP-block was
  supposed to refuse. Operators with broken DNS will now see an
  unambiguous "DNS resolution failed" error at remote-cluster
  registration / connection time and at admin-UI settings updates,
  rather than an SSRF-shaped silent success.

- **Dev PostgreSQL compose loopback bind** (audit P3-8). The
  developer compose file's port mapping changed from `5432:5432` (LAN
  reachable) to `127.0.0.1:5432:5432` (loopback only). Credentials are
  now sourced from `POSTGRES_PASSWORD` / `POSTGRES_USER` /
  `POSTGRES_DB` environment variables (or `.env` file) with the
  previous weak defaults preserved as fall-throughs for `make dev-db`.
  Operators whose developer DB was being accessed from other LAN hosts
  must now configure that connection through SSH tunnels or change the
  bind explicitly. Production and staging Postgres deployments are
  unaffected — they use the Helm chart, not this compose file.

### Non-breaking changes

- **SSRF-safe DialContext on long-lived HTTP transports** (audit P2-6
  part 2). New `k8s.SafeHTTPTransport` and `k8s.SafeDialContext`
  wired into:
  - Remote-cluster `rest.Config.Dial` (cluster_router) — every k8s
    API call through the impersonating clients re-validates the API
    server IP, defending against DNS rebinding and the cluster URL
    flipping to an internal address mid-session.
  - Grafana reverse proxy transport — each Grafana 30x redirect
    re-runs the IP check, so a misbehaving Grafana returning
    `Location: http://169.254.169.254/` can't pivot the proxy.
  - Grafana API client and Prometheus API client transports — same
    protection for dashboard provisioning and PromQL queries.

  Behaviour: each TCP dial re-resolves the host, pins to the
  validated IP before dialing (a racy resolver can't substitute a
  private IP), and rejects any candidate in the loopback / RFC1918 /
  link-local-metadata / CGNAT / unspecified ranges.

- **Hostname-resolving validation on admin-UI URL inputs** (audit
  P2-6 part 1). `validateSettingsURL` previously only blocked literal
  IPs. Settings paths (monitoring Prometheus / Grafana URLs, OIDC
  issuer test, LDAP server test) now resolve the hostname and apply
  the full SSRF block-list. Admins who need to point monitoring at
  an in-cluster service should configure it via Helm values rather
  than the UI.

- **NetworkPolicy backend egress destinations** (audit P2-7). The
  previous port-only egress rules let the backend reach any host on
  the LAN, public internet, or in-cluster on the allowed ports.
  Tightened to structured `to:` selectors with universal SSRF block:
  - DNS: `namespaceSelector kube-system + podSelector k8s-app=kube-dns`
    (tunable via `networkPolicy.egress.dnsNamespace/dnsPodLabel`).
  - Kubernetes API: `ipBlock` allowlist via
    `networkPolicy.egress.kubernetesApiCIDRs`, falling back to
    `0.0.0.0/0 minus link-local/metadata` when empty.
  - In-cluster Postgres / Prometheus / Grafana: `podSelector` by
    standard `app.kubernetes.io/name` labels.
  - External monitoring: `networkPolicy.egress.monitoringCIDRs` (opt-in).
  - LDAP: `networkPolicy.egress.ldapCIDRs` (opt-in).
  - Extra allowed: `networkPolicy.egress.extraAllowedCIDRs`.
  - Extra blocked: `networkPolicy.egress.extraBlockedCIDRs` (appended
    to the universal link-local + metadata block on every IP rule).

  Default values preserve current reachability for homelab / single-
  cluster deployments; operators with out-of-cluster dependencies opt
  into the new knobs.

- **Test seam: `NewPrometheusClientWithTransport`**. Production code
  continues to use `NewPrometheusClient`, which wires
  `SafeHTTPTransport` by default. The new variant accepts a caller-
  supplied `http.RoundTripper` so tests using `httptest.Server` URLs
  on loopback (which the safe-by-default transport correctly refuses)
  can construct the client. The doc comment is explicit that
  production callers must keep using the safe-by-default constructor.

### Applied during Phase 4 code review (one round of `/ce-code-review`)

The review surfaced several issues that were fixed inline before merge:

- **Split SafeDialContext / StrictDialContext** (correctness C1,
  testing TG-1, maintainability M-2). The original blanket RFC1918
  block would have silently broken every operator running
  `monitoring.deploy=true` because in-cluster Service ClusterIPs are
  RFC1918. SafeDialContext (used by the monitoring clients) now
  allows RFC1918 while still blocking loopback / link-local-metadata /
  CGNAT / unspecified. StrictDialContext (used by cluster_router for
  remote API server URLs) keeps the full block-list including RFC1918.
- **`monitoring_test.go` migrated to NewPrometheusClientWithTransport**
  (testing TG-1, maintainability M-2). I missed two call sites when
  adding the test seam in commit a0a4a9e.
- **Insecure-exposure ack string bypass** (adversarial, reliability
  R-5). `--set-string security.insecureExposureAcknowledged="false"`
  previously bypassed the guard because Helm's `not` evaluates non-
  empty strings as truthy. Now coerces through `toString | eq "true"`.
- **`ingress.tls: [{}]` empty-map bypass** (adversarial, reliability
  R-5). A single empty-map entry passed `not (empty)` and rendered
  an Ingress with `secretName: ""`. Now requires at least one entry
  with a non-empty secretName.
- **NetworkPolicy podSelector cross-namespace breakage** (correctness
  C3, reliability R-3, adversarial 4). Bare podSelector matches only
  same-namespace pods. Added `namespaceSelector: {}` to Postgres,
  Prometheus, and Grafana egress rules so the typical
  Prometheus-in-`monitoring`-namespace layout works.
- **External Postgres silently blocked** (reliability R-3). Operators
  using `externalDatabase.host` without populating
  `networkPolicy.egress.extraAllowedCIDRs` lost DB connectivity. Added
  a port-5432 ipBlock fallback with the universal link-local except.
- **`networkPolicy.egress.postgresPodLabels` knob** (adversarial 7).
  Defaults to bitnami convention; operators on CrunchyData / CNPG /
  Zalando override the label map.
- **DNS values comment expanded** (reliability R-2, adversarial 5).
  Calls out Talos / RKE2 / OpenShift label divergences explicitly.
- **`ValidateRemoteURL` context+timeout** (reliability R-1). Previous
  `net.LookupHost` had no deadline and could stall goroutines for
  ~90s. New `ValidateRemoteURLContext` propagates caller context;
  the no-arg shim applies a 5s deadline so existing call sites get
  fail-fast behavior without a refactor.

### Known follow-ups (Phase 4 deferred — single review round)

This phase follows the Phase 3 cadence: one round of `/ce-code-review`
plus this documented follow-up list rather than chasing every regression
through additional cycles. The deferred items below are tracked here
for the next audit:

- **Loki HTTP client missed SafeHTTPTransport** (reliability R-4).
  `internal/loki/client.go` still uses bare `&http.Transport{}` while
  every other monitoring client was migrated. Same DNS-rebinding gap
  applies post-registration. Low-effort follow-up.
- **SafeDialContext IPv6 dual-stack pinning** (correctness C2).
  Current implementation pins to `ips[0]` — fine for single-stack
  hosts but forecloses on happy-eyeballs preference for dual-stack
  resolvers. Acceptable trade-off (every candidate is validated to
  be non-blocked) but iterating the validated list would preserve
  both protections and Happy Eyeballs.
- **NAT64-wrapped metadata IPs not blocked** (adversarial 3).
  `64:ff9b::169.254.169.254` evades `IsLinkLocalUnicast()`. Narrow
  attack surface (IPv6-only/NAT64 AWS deployments) but worth a
  custom-CIDR check in the next pass. NetworkPolicy `except` block
  also only lists IPv4 `169.254.0.0/16`.
- **`validateSettingsURL` wrapper can be inlined** (maintainability
  M-1). After delegating to `k8s.ValidateRemoteURL`, the wrapper is
  three lines of trivial dispatch and the string-sentinel return type
  is inconsistent with the OIDC/LDAP handlers in the same file.
- **`validateSettingsURL` admin-UI .svc DNS rejection** (correctness
  C4). Operators trying to set `http://prometheus.monitoring:9090`
  via the UI get an opaque "URL resolves to private address" error.
  Workaround: configure via Helm values instead. UI hint or per-URL
  trust knob could improve the UX.
- **DNS-rebinding integration tests** (testing TG-2/TG-3/TG-4, RR-1).
  Core security claim — hostname resolves to private IP at dial time —
  is not covered by direct tests. Requires injecting a fake resolver
  (package-level `lookupHost` var or `SafeDialer` struct with resolver
  field). Literal-IP, DNS-failure, and IP-pin TOCTOU branches are
  also untested.
- **Helm `_validate.tpl` no negative-case CI coverage** (testing TG-5).
  Three new fail() branches with no automated tests asserting they
  fire. Phase 4 verified by manual matrix; future CI should run a
  `helm template` per fail condition.
- **`safeDialerTimeout` / `safeDialerKeepAlive` unexported** (M-3).
  Three sibling 30s hardcoded values in the monitoring subsystem
  cannot reference the dialer constants. Either export them or add
  a comment cross-reference.
- **NetworkPolicy ipBlock `except` block duplicated 6 times** (M-4).
  Helm doesn't have a clean partial-template extraction for the
  except stanza alone. Mitigated by sharing the `$blockedRanges` var
  but the rendering loop repeats. Worth a comment flagging the
  parallel-edit requirement.
- **Cookie Secure flag still gated on global Dev flag, not per-request
  loopback.** `Secure: !s.Config.Dev` means an admin who bypasses the
  chart guard with `security.insecureExposureAcknowledged=true` AND
  runs dev mode will ship non-Secure cookies. Acceptable given the
  explicit acknowledgement.
- **Monitoring URL validation at boot-time helm/env path.** Operator-
  set URLs via Helm bypass `validateSettingsURL`. The dial-time check
  still applies but boot-time fail-fast would surface
  misconfiguration earlier.
- **`internal/monitoring/discovery.go:263` pre-existing unusedparams
  diagnostic.** Not introduced by Phase 4.

---

## Phase 3 — Auth primitives hardening (2026-05 audit)

Phase 3 closes audit findings P2-1 (trusted-proxy CIDR boundary +
per-account/login-name throttle), P2-2 (atomic refresh-token rotation),
and P2-3 (LDAP revalidation on refresh + 1h LDAP refresh-token cap).
The audit report is `plans/security-audit-2026-05-22.md` (same source
as Phase 2; Phase 3 closes the remaining auth-primitive findings).

### Breaking changes (operator action required)

- **LDAP refresh-token TTL: 7 days → 1 hour** (audit P2-3). LDAP-sourced
  sessions now cap refresh-token lifetime at 1 hour, mirroring the OIDC
  cap added in Phase 2. Combined with the new per-refresh revalidation
  against the directory, this bounds the worst-case revoked-LDAP-user
  access window to one access-token lifetime (~15 minutes) plus the
  5-minute transient-outage grace window. Operators relying on long-
  lived LDAP refresh sessions must adjust client retry / re-login
  behaviour. See `backend/internal/auth/jwt.go` `LDAPRefreshTokenLifetime`.

- **LDAP revalidation on every refresh** (audit P2-3). The refresh
  handler now binds to the configured LDAP directory and re-fetches
  group membership on every refresh attempt for LDAP-sourced sessions.
  This means: (a) the LDAP server sees one bind+search per active
  user's refresh interval (~15 minutes); (b) a directory-side LDAP
  outage longer than 5 minutes evicts all LDAP users until the
  directory recovers. The 5-minute bounded grace window preserves
  last-known identity during brief outages. Operators with high LDAP
  client counts should size the directory accordingly.

- **`KUBECENTER_SERVER_TRUSTEDPROXYCIDRS` config** (audit P2-1). The
  global middleware no longer trusts X-Forwarded-For / X-Real-IP from
  every peer (the chi-RealIP default). Operators behind a load
  balancer or ingress controller MUST configure this CIDR allowlist
  or rate-limit buckets and audit `SourceIP` fields will key on the
  proxy's address rather than the client's. Default empty =
  fail-closed (no proxies trusted). Narrow the trust set to the
  exact ingress-controller pod or service CIDR — broad ranges (e.g.
  the full Kubernetes pod CIDR) let any in-cluster workload spoof the
  header. The middleware emits a startup WARN on `/0` catch-all
  entries that reinstate the pre-Phase-3 blanket-trust behaviour.

### Non-breaking changes

- **Atomic refresh-token rotation** (audit P2-2). The Peek+Consume
  split that allowed two concurrent refreshers to each mint a
  successor pair from one stolen refresh token has been replaced by
  an atomic `SessionStore.Rotate` (sync.Map.LoadAndDelete-backed).
  Concurrent refresh attempts on the same token can no longer both
  succeed; the loser receives 401. Transient post-rotate failures
  (issueTokenPair signing error, LDAP transient-outside-grace) call
  `SessionStore.Restore` so the client can retry rather than being
  silently logged out. No operator action.

- **Per-account login + setup throttle** (audit P2-1 part 2). New
  per-username rate-limit bucket layered on top of the existing
  per-IP throttle. An attacker rotating source IPs against a single
  username (or iterating likely admin names against `/setup/init`)
  now hits the account bucket regardless of source IP. No operator
  action; budget shares the global 5/min default.

- **Audit-emit coverage**. handleRefresh body-mode missing-token,
  user-not-found, and issueTokenPair-failure paths now emit audit
  entries; handleSetupInit setup-token mismatch now audits the 403.
  Earlier branches were silent, hiding brute-force probing from the
  audit table.

### Known follow-ups (filed during Phase 3 ce-code-review)

These are documented review findings deferred to follow-up PRs to
keep Phase 3 focused. None are blockers for shipping Phase 3.

- Credential-login (`/auth/login`) body-mode is missing — agents using
  local/LDAP credentials cannot retrieve the refresh token at login
  time. (agent-native AN-C1, P1, conf 100)
- `/auth/refresh` 401 responses lack a stable `error.Reason`
  discriminator across five semantically distinct failure modes.
  (agent-native AN-W1, P1)
- Audit query API has no detail-text filter — monitoring agents
  detecting LDAP-grace-window thrashing must client-side-filter.
  (agent-native AN-W2, P2)
- handleLogout revokes only the inbound token; a concurrent refresh
  can leave an orphaned rotated successor session that survives until
  ExpiresAt. (adversarial adv-2, P2)
- Per-account throttle key is case-folded but not Unicode-normalised;
  trailing whitespace + zero-width characters dilute the throttle.
  (security sec-2 + adversarial adv-3, P3)
- `ldapGracePeriod` is a hardcoded 5-minute constant — not
  config-tunable. (maintainability MAINT-003, P2)
- LDAP `Revalidate` context cancellation does not propagate to the
  in-flight LDAP ops (10s wall-clock bound only). (reliability
  REL-02, P2)
- No singleflight coalescing on `Revalidate` for the same userDN;
  mass-refresh after backend restart hits the directory N times per
  user. NOTE: the learnings researcher flagged this as advisory only
  — the Phase 2 singleflight pattern in cluster_router does NOT
  transfer cleanly to per-session LDAP. (reliability REL-03, advisory)

---

## Phase 2 — Multi-cluster RBAC & TLS hardening (2026-05 audit)

The Phase 2 security audit produced three review rounds (2026-05-13,
2026-05-17, 2026-05-22) plus a round-3 best-judgment pass on 2026-05-23.
Findings and the patches that closed them are linked from
`docs/security/2026-05-22-audit-report.md`.

### Breaking changes (operator action required)

- **`allow_insecure_tls` column on `clusters`** (round-1 F#5; round-3 F#2).
  cluster_router.buildRemoteConfig no longer silently sets
  `TLSClientConfig.Insecure = true` when no CA data is stored. Admins must
  explicitly opt in by registering the cluster with `allowInsecureTLS=true`
  (admin role required) or by providing a CA certificate.

  - Migration `000016_cluster_allow_insecure_tls.up.sql` adds the column.
  - Migration `000017_backfill_allow_insecure_tls.up.sql` (round-3 F#2)
    auto-flags every existing CA-less non-local cluster as
    `allow_insecure_tls=true` to preserve pre-upgrade connectivity. Review
    the flagged rows in the cluster list (red "⚠ Insecure TLS" badge) and
    either supply a CA or accept the flag explicitly. See
    `backend/internal/store/migrations/NOTES.txt` for the inspection query
    and full operator runbook.
  - Down migrations are RETAINED for golang-migrate convention but are
    NOT a safe standalone rollback path — see the binary-rollback
    constraint block in `000016.down.sql`. Rolling back to a pre-F#5
    binary reintroduces the silent TLS-skip vulnerability the change
    was designed to close.

- **Slug-endpoint admin gate** (round-1 F#11). Endpoints that take a
  cluster slug (rather than the cluster's UUID) now require admin role
  even when the slug resolves to the local cluster. Non-admin tooling
  that hardcoded slug paths must switch to the UUID form.

- **Grafana proxy method narrowing** (round-1 F#14). The
  `/api/v1/monitoring/grafana/proxy/*` route now accepts only `GET` and
  `HEAD`. Dashboards or external integrations that previously POST'd to
  the proxy must switch to a server-side Grafana API call.

- **`MobileAuthConfig` JSON tag rename** (mobile M5 PR-5j, #282).
  `clientID → clientId` on the `/v1/auth/oidc/{providerID}/mobile-config`
  endpoint response. Public-store mobile builds must consume the new key
  before the wire format ossifies. Backend Go field name `ClientID`
  retained per Go style. This change is not strictly a security audit
  output but is shipping in the same window.

### Non-breaking (visibility, observability, defense-in-depth)

- **WebSocket confused-deputy gates** (round-1 P2-5, round-3 F#1).
  `handle_ws_logs`, `handle_ws_flows`, `handle_ws_logs_search` reject
  non-local `X-Cluster-ID` headers BEFORE the RBAC check or the
  upstream stream open. Audit `ResultFailure` recorded on rejection.

- **Singleflight ctx-cancel hardening** (round-2 F#17, round-3 F#3/F#6/F#15).
  cluster_router.remoteConfig and certmanager.fetchAllRemote use
  `context.WithoutCancel` so one HTTP caller's disconnect cannot poison
  every other coalesced waiter. 30s default cap when the caller did
  not set a deadline.

- **Cert-manager EvictRemoteCache hook** (round-3 F#8). Cluster deletion
  or credential update wipes the per-cluster cert cache in the same
  operation, preventing a brief leak of the previous tenant's cert list
  through a re-registered cluster ID.

- **Connection-test routes through ApplyClusterTLS** (round-3 F#13). The
  cluster-registration test exercises the same fail-closed TLS policy
  as the runtime router and probe; no policy drift between the two
  paths can silently slip through register-time.

- **OIDC refresh lifetime cap at 1h** (mobile M5 PR-5b). Propagates IdP
  revocation (account disabled, group removed) within an hour rather
  than waiting for the standard 7-day rotation cycle.

- **Audit logging on every WS rejection path** (round-2 F#7). The audit
  table — what compliance reviews actually read — now records both
  successful subscribes and rejected attempts. Previously the only
  forensic record of a rejected remote-cluster WS attempt was a slog
  warning.

### Files / paths

- Backend security audit report: `docs/security/2026-05-22-audit-report.md`
- Migration NOTES: `backend/internal/store/migrations/NOTES.txt`
- TLS policy single source of truth: `backend/internal/k8s/cluster_router.go`
  (`applyClusterTLS` + `ApplyClusterTLS`)

---

## Pre-Phase-2 history

Pre-Phase-2 changes were tracked only in commit history and per-phase plan
docs (`plans/`). Starting from this audit cycle, every breaking change or
security-impactful patch lands in this file as well.
