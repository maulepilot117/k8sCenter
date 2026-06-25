# Backend Fuzzing — PR #2 (Layer B HTTP-handler harness + oracles C/D) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Layer B HTTP-handler fuzzing harness (a new `internal/server/fuzztest` package) and the first two security-oracle targets — auth/authz (oracle C) and secret-leak masking (oracle D) — fuzzing real handlers through the genuine middleware chain with a fake k8s client.

**Architecture:** A reusable `fuzztest` support package builds a `server.Server` via the exported `server.New(Deps{...})`, generalizing the existing `testServer(t)` helper. It mints JWTs through `auth.TokenManager.IssueAccessToken`, drives requests via `Router.ServeHTTP` + `httptest` (mirroring the existing `doRequest` helper), and injects a fake dynamic k8s client through `ClientFactory`'s existing `testDynOverride dynamic.Interface` seam. Fuzz targets live in `_test.go` files in `package fuzztest`, feed Go fuzz corpora onto request body/headers/path, and assert oracle A (crash-safety) plus C or D.

**Tech Stack:** Go 1.26 native fuzzing (`testing.F`), `net/http/httptest`, `k8s.io/client-go/dynamic/fake`, chi router, existing `server`/`auth`/`k8s` packages.

**Origin:** `docs/superpowers/specs/2026-06-25-backend-fuzzing-design.md` (Layer B = Approach 1; oracles C = auth/authz, D = secret-leak). PR #1 (Layer A) merged as `461209af`.

> **Status note (post-pivot, 2026-06-25):** During U1, Secrets were found to use the typed `*kubernetes.Clientset` (not the dynamic-client seam), so the planned `fuzztest` HTTP-handler harness was dropped (commit `984ed139`). As built: oracle C → `FuzzAuthzEnforcement` in `package server` (`backend/internal/server/authz_fuzz_test.go`, reusing `testServer`); oracle D → `FuzzMaskedSecret` in `package resources` (`backend/internal/k8s/resources/secret_mask_fuzz_test.go`, fuzzing the pure `maskedSecret` function). CI matrix rows point at `./internal/server/` and `./internal/k8s/resources/`. The Output Structure / Files sections below describe the original (superseded) `fuzztest` design.

---

## Global Constraints

- Go version floor: **Go 1.26** (matches `backend/go.mod`). Native fuzzing only; **no new third-party dependencies** (`client-go` fake is already a transitive dep of the project — confirm it resolves without a `go.mod` addition; if it requires a new require line, verify the version is the one already in `go.sum`).
- Repo-wide verification before push (CLAUDE.md Rule 4): `cd backend && go vet ./... && go test ./...` — tree-wide, not scoped.
- Fuzz targets must not perform real network I/O, hit a real cluster, or require Postgres. The fake dynamic client and in-memory auth/session stores are the only backends.
- Oracles assert invariants only on the **success/violation** branch; a rejected/`4xx` response to malformed input is acceptable (it is the desired behavior for oracles C/D).
- `testdata/fuzz/` is gitignored (PR #1 `7d91e632`); do not commit coverage corpus.
- CI: the seed corpora run under the existing `go test ./...` gate; deep-fuzz integrates into the existing `.github/workflows/fuzz.yml` matrix.
- Branch from `main`; never commit to `main`. Work on a `feat/backend-fuzzing-pr2` branch.
- Secret-data-destruction defense (CLAUDE.md mobile invariant, applies to any Secret seed): fixtures must use real-looking secret values so oracle D can detect leakage; never seed the literal `****` mask as the secret.

---

## Output Structure

```
backend/internal/server/fuzztest/
├── harness.go          # NewServer(t, Opts) *server.Server; Opts{WithResources bool, Secrets []*unstructured.Unstructured, Users ...}
├── tokens.go           # MintAccessToken(t, tm, user) / expired / malformed helpers
├── request.go          # BuildRequest(corpus) -> *http.Request mapping fuzz bytes onto body/headers/path/query
├── authz_fuzz_test.go  # FuzzAuthzEnforcement (oracle C)
└── secret_leak_fuzz_test.go # FuzzSecretMasking (oracle D)
```

The three non-test files form the reusable harness (a test-support package that imports `testing` — acceptable for a `*test`-suffixed support package never imported by production code). The two `_test.go` files hold the fuzz targets. Per-unit `**Files:**` are authoritative; the tree is the intended shape.

---

## High-Level Technical Design

Request flow under fuzz (oracle C and D share this spine):

```
fuzz corpus bytes
  -> fuzztest.BuildRequest  (maps bytes -> method/path/body/headers, incl. attacker-controlled
                             Authorization, X-Requested-With, X-Cluster-ID, Impersonate-* )
  -> server.Router.ServeHTTP  (REAL chain: Timeout -> Auth(TokenManager) -> CSRF -> ClusterContext -> handler)
  -> httptest.ResponseRecorder
  -> oracle:
       A (always): handler did not panic; responded within timeout
       C (authz):  unauthenticated/under-privileged -> never 2xx on a protected route;
                   state-change without X-Requested-With -> rejected (403);
                   client Impersonate-* headers -> effective identity unchanged
       D (secret): response body never contains the injected sentinel secret in cleartext
```

Harness wiring decision (the load-bearing one): `server.New` only constructs `ResourceHandler` when **both** `K8sClient != nil && Informers != nil` (`server.go:166`). Oracle D needs a live resource route. Two candidate wirings are evaluated in U1 (see Open Questions); the plan does not pre-commit.

---

## Key Technical Decisions

1. **Separate `fuzztest` package, not in-package `server` test helpers.** Oracle C/D targets drive handlers through the public `Router.ServeHTTP`, so they need no unexported `server` access. A standalone support package keeps the harness reusable by PR #3+ and by both `package server` tests and the fuzz targets. Rationale: the existing `testServer`/`doRequest` helpers are trapped in `handle_auth_test.go` (test-only, package-private); generalizing them into `fuzztest` makes them importable.
2. **Inject fakes via the existing `testDynOverride` seam, not a new abstraction.** `ClientFactory` already exposes `testDynOverride dynamic.Interface` ("if set, `DynamicClientForUser` returns this directly"). `dynamicfake.NewSimpleDynamicClient(scheme, objs...)` satisfies it. This avoids touching production code. The typed `testOverride *kubernetes.Clientset` is concrete (can't take `fake.Clientset`), so oracle D targets the **dynamic-client** read path for Secrets.
3. **Oracle C asserts on the success branch only.** A fuzzed request that happens to be well-formed and authorized may legitimately return 2xx — the oracle fires only when the request is provably unauthenticated/under-privileged or CSRF-less. The harness controls auth state explicitly (mint vs omit token) so the oracle knows the expected authorization outcome.
4. **Fuzz input → request mapping is structured, not raw-body-only.** `BuildRequest` splits the corpus into a small struct (path selector, body bytes, header toggles) so a single `[]byte`/`string` fuzz argument exercises body, headers, and path variation — maximizing coverage of the decode + middleware + handler chain rather than only JSON bodies.
5. **Reuse the in-repo `upload-artifact` SHA + matrix shape** from `fuzz.yml` (established PR #1) when adding the two new targets to the nightly matrix — no new CI structure.

---

## Implementation Units

### U1. `fuzztest` harness package

**Goal:** A reusable package that builds a real `server.Server` (auth-only and with-resources modes), mints tokens, seeds fake Secrets, and drives `httptest` requests.

**Requirements:** Enables oracles A/C/D (design spec "Layer B — Approach 1"). Prerequisite for U2, U3.

**Dependencies:** none.

**Files:**
- Create: `backend/internal/server/fuzztest/harness.go`
- Create: `backend/internal/server/fuzztest/tokens.go`
- Create: `backend/internal/server/fuzztest/request.go`
- Test: `backend/internal/server/fuzztest/harness_test.go` (smoke test: build both server modes, one authed + one unauth request return expected status)

**Approach:**
- Generalize `testServer(t)` (`handle_auth_test.go:24`): build `Deps{Config, Logger, TokenManager, LocalAuth, AuthRegistry, OIDCStateStore, Sessions, AuditLogger, RateLimiter, ReadyFn}` for auth-only mode.
- For `WithResources` mode, additionally construct a `ClientFactory` with `testDynOverride` set to `dynamicfake.NewSimpleDynamicClient(...)` seeded with `Opts.Secrets`, plus the `InformerManager` (see Open Questions for the informer-vs-direct decision — U1 resolves it and documents the chosen wiring in the package doc comment).
- `MintAccessToken(t, tm, user)` wraps `IssueAccessToken`; add `MintExpiredToken` / `garbageToken` constant for the negative cases.
- `BuildRequest(corpus []byte) *http.Request`: deterministic mapping of corpus → method/path/body/headers (incl. optional `Authorization`, `X-Requested-With`, `X-Cluster-ID`, `Impersonate-User`/`Impersonate-Group`).

**Patterns to follow:** `backend/internal/server/handle_auth_test.go` (`testServer`, `doRequest`, `loginAdmin`); `backend/internal/k8s/client.go` (`testDynOverride` seam); any existing `dynamicfake` usage found via grep before writing.

**Execution note:** Start with the `harness_test.go` smoke test (build server, one authed 2xx + one unauth 401) so the package compiles and wires correctly before any fuzz target depends on it.

**Test scenarios:**
- Happy path: `NewServer(t, Opts{})` returns a server whose `/api/v1/auth/me` returns 401 without a token and 2xx with a freshly minted admin token.
- With-resources: `NewServer(t, Opts{WithResources:true, Secrets:[...]})` registers resource routes (a GET on the seeded Secret path returns 2xx for an authed admin).
- Edge: `BuildRequest` on empty corpus and on a 1-byte corpus produce a valid `*http.Request` without panicking.
- `Test expectation:` harness code is exercised entirely through U2/U3 fuzzers + this smoke test.

**Verification:** `go test ./internal/server/fuzztest/ -run TestHarness -v` passes; package compiles under `go vet`.

### U2. Oracle C — auth/authz enforcement fuzz target

**Goal:** Fuzz protected handlers (auth/me, settings, users) asserting unauthenticated requests never get 2xx, CSRF-less state changes are rejected, and client `Impersonate-*` headers are never honored.

**Requirements:** Oracle C (design spec).

**Dependencies:** U1.

**Files:**
- Create: `backend/internal/server/fuzztest/authz_fuzz_test.go`

**Approach:**
- `FuzzAuthzEnforcement(f *testing.F)` seeds: no-token GET to a protected route; valid-token POST without `X-Requested-With`; request carrying `Impersonate-User: system:admin`; malformed `Authorization` values; oversized body.
- Oracle: parse the corpus into (auth-state, method, route, headers). When auth-state is "no/invalid token" and route is protected → assert status != 2xx. When method is state-changing and `X-Requested-With` absent → assert 403. When `Impersonate-*` present → assert the effective server identity (observable via the response or an echo route) is unchanged from the token identity.
- Confirm the exact CSRF rejection contract by reading `middleware/csrf.go` first (Open Question OQ2).

**Patterns to follow:** `middleware.Auth`, `middleware.CSRF`, `middleware.ClusterContext` (`routes.go:109-112`); existing `handle_settings_test.go` / `handle_users_test.go` for protected-route shapes.

**Test scenarios:**
- Covers oracle C. Unauthenticated GET `/api/v1/auth/me` → never 2xx (seed).
- State-changing POST to `/api/v1/settings` (or `/users`) without `X-Requested-With` → 403 (seed).
- Authed request with `Impersonate-User`/`Impersonate-Group` headers → identity unchanged (seed). Teeth: this fails if a handler ever trusts client impersonation headers.
- Crash-safety (oracle A): no panic / no timeout on any corpus.
- Error path: garbage/expired `Authorization` header → 401, never 2xx.

**Verification:** `go test ./internal/server/fuzztest/ -run=FuzzAuthzEnforcement` (seed corpus) passes; a 30s `-fuzz` run finds no oracle violation.

### U3. Oracle D — Secret-masking fuzz target

**Goal:** Fuzz the Secret-returning GET handler asserting the response body never contains an injected sentinel secret value in cleartext under malformed input.

**Requirements:** Oracle D (design spec).

**Dependencies:** U1.

**Files:**
- Create: `backend/internal/server/fuzztest/secret_leak_fuzz_test.go`

**Approach:**
- Seed the fake dynamic client (via U1 `Opts.Secrets`) with a Secret whose `data` contains a known sentinel (e.g. `c2VudGluZWwtc2VjcmV0` = base64 "sentinel-secret"). Mint an authed admin token.
- `FuzzSecretMasking(f *testing.F)`: fuzz the request path/namespace/name/query around the Secret GET route.
- Oracle: for any response (2xx or not), assert the body never contains the decoded sentinel (`sentinel-secret`) nor its raw base64 form — masking in `resources/secrets.go` must hold. Confirm the masking contract + the exact GET route by reading `resources/secrets.go` and `routes.go` resource registration first (OQ3).

**Patterns to follow:** `backend/internal/k8s/resources/secrets.go` (masking logic), `resources_test.go` (Secret fixtures).

**Execution note:** Characterization-first — read `secrets.go` and capture the exact masked-response shape before asserting, so the oracle matches real masking behavior rather than an assumed one.

**Test scenarios:**
- Covers oracle D. Authed GET of the seeded Secret → response masks the sentinel (`****`), body does not contain `sentinel-secret` (seed).
- Fuzzed namespace/name/query around the route → body never leaks the sentinel (any status).
- Crash-safety (oracle A): no panic on malformed path/query.
- Edge: reveal/unmask query params (if the route supports them) still never leak without the explicit reveal contract — confirm against `secrets.go`.

**Verification:** `go test ./internal/server/fuzztest/ -run=FuzzSecretMasking` passes; 30s `-fuzz` run finds no leak.

### U4. CI matrix integration

**Goal:** Add the two new targets to the nightly deep-fuzz matrix.

**Requirements:** Design spec CI section ("scheduled deep fuzz").

**Dependencies:** U2, U3.

**Files:**
- Modify: `.github/workflows/fuzz.yml`

**Approach:** Add two matrix rows: `{ pkg: ./internal/server/fuzztest/, target: FuzzAuthzEnforcement }` and `{ ..., target: FuzzSecretMasking }`. The existing `Verify … exists` guard, `-fuzztime=5m`, and `upload-artifact` steps (PR #1) apply unchanged.

**Patterns to follow:** existing matrix rows in `.github/workflows/fuzz.yml`.

**Test scenarios:** `Test expectation: none — CI config.` Verify via `python -c "import yaml; yaml.safe_load(...)"` and confirm both target names resolve with `go test ./internal/server/fuzztest/ -list '^Fuzz.*$'`.

**Verification:** YAML parses; both targets appear in `-list`; the matrix `Verify … exists` guard would pass for both.

---

## Scope Boundaries

**In scope:** the `fuzztest` harness; oracle C over auth/me + settings + users; oracle D over the Secret GET; CI matrix rows.

### Deferred to Follow-Up Work
- Additional oracle-C coverage over the full protected route set (only auth/me + settings + users in this PR).
- Oracle D over other secret-bearing responses (audit logs, ESO, cert-manager) — pattern established here, breadth later.
- Wizard/YAML apply-path fuzzing (write paths) — separate effort.

### Out of scope (other PRs)
- Layer A breadth (PR #3): the other four SSRF validators, `parsePHC`, `ParseMultiDoc`, `unstructured` normalizers.
- Remote-cluster / multi-cluster fuzzing (needs Postgres-backed ClusterStore — violates the no-Postgres constraint).

---

## Open Questions (Implementation-Time Unknowns)

- **OQ1 — Informer requirement for `ResourceHandler` (resolve in U1).** `server.New` needs `Informers != nil` to build `ResourceHandler`. Options: (a) construct a minimal `InformerManager` backed by the fake dynamic client; (b) determine whether the Secret GET path reads from the informer cache or does a direct `DynamicClientForUser` call, and if direct, satisfy `Informers` with a trivial non-nil manager that the Secret path never touches. U1 reads `resources/secrets.go` + `informers.go` and picks the minimal wiring; documents it in the package doc comment. This gates oracle D.
- **OQ2 — Exact CSRF rejection contract (resolve in U2).** Confirm `middleware/csrf.go` rejects state-changing requests lacking `X-Requested-With: XMLHttpRequest` with 403 (CLAUDE.md says so; verify the status code and which methods are exempted) before asserting.
- **OQ3 — Secret GET route + masking shape (resolve in U3).** Confirm the exact route (`GET /resources/secrets/:ns/:name` or similar) and the masked response shape in `resources/secrets.go` (is it `****`, empty, or omitted?) so the leak oracle checks the right invariant and the reveal/unmask contract.
- **OQ4 — `dynamicfake` GVR/scheme registration.** The fake dynamic client needs the Secret GVR registered in its scheme and list-kind mapping; confirm the exact registration pattern (grep for any existing `dynamicfake` use, else follow client-go docs) during U1.

---

## Risks & Dependencies

- **Informer wiring (OQ1) is the main risk.** If neither minimal-informer nor direct-path wiring is tractable, oracle D (U3) may need to target the masking function more directly (a thinner Layer-A-style test) — fall back documented, but try the full handler path first since oracle D's value is proving masking holds *through the real response pipeline*.
- **False-positive oracle C:** asserting non-2xx for a request the harness *thinks* is unauthorized but is actually well-formed/authorized. Mitigated by KTD 3 (harness controls auth state explicitly).
- **No new dependency:** `client-go/dynamic/fake` must already be in `go.sum`. If not, adding it requires a cooldown check — flag and stop per the supply-chain rule rather than silently adding.

---

## Verification (whole-PR gate)

- `cd backend && go vet ./...` exit 0; `go test ./...` all green (seed corpora run here).
- Each new target survives a local 30s `-fuzz` run with no oracle violation.
- `fuzz.yml` parses; both new targets resolve via `-list`.
- Per-task reviews + final whole-branch review (subagent-driven-development), then `/ce-review` before merge.
