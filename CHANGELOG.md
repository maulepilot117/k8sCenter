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
