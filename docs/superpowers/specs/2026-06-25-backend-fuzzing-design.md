# Backend Fuzzing — Design

**Date:** 2026-06-25
**Status:** Design (pending implementation plan)
**Scope:** Go backend (`backend/`). Frontend/mobile out of scope.

## Goal

Add fuzz testing to the Go backend to harden reliability **and** security across the
API surface. Fuzzing complements the existing 223 example-based unit tests by
exercising the input-parsing seams with adversarial/random input to surface panics,
hangs, unbounded allocations, and — via explicit oracles — security-property
violations (auth bypass, injection, SSRF evasion, secret leakage).

Decisions locked during brainstorming:

- **Goal:** Both reliability and security, balanced (started at the parsing seams).
- **Layers:** A (pure parse/transform functions via Go native fuzzing) **+** B (HTTP
  handler layer via `httptest` + fake k8s client). Layer C (full black-box stack
  fuzzing) is explicitly deferred.
- **CI model:** Gate + scheduled deep fuzz. Seed corpus runs as normal unit tests on
  every PR; a scheduled job runs each target with a time budget and reports failures.
- **Oracles (all four):**
  - **A — Crash-safety:** no panics, no hangs/timeouts, no unbounded memory. Baseline,
    always on.
  - **B — Parser invariants:** round-trip (`parse(format(x)) == x`), idempotency
    (`f(f(x)) == f(x)`), and "validate-then-use never crashes".
  - **C — Auth/authz oracles (Layer B):** unauthenticated requests never get 2xx on
    protected routes; state-changing requests without CSRF header are rejected;
    client-supplied impersonation headers are never honored.
  - **D — Secret-leak oracle (Layer B):** no response body ever contains an unmasked
    secret value under malformed input — the `****` masking holds.
- **Layer B structure:** Approach 1 — targeted handler-level fuzzing through the real
  middleware chain with a fake `client-go` clientset/dynamic client. No real cluster,
  no Postgres.

## Why fuzzing fits this codebase

The backend has many functions that parse untrusted, structured input. Go 1.26 ships
native coverage-guided fuzzing (`go test -fuzz`), so targets live next to existing
tests, the seed corpus doubles as regression tests, and there is no new tooling
dependency. Several of the highest-value targets already carry a natural security
oracle (e.g. `loki.EnforceNamespaces` must never emit a query that escapes the allowed
namespace set), which makes them ideal fuzz targets rather than mere crash tests.

## Architecture

### Layer A — pure-function fuzzing

Fuzz targets are added as `Fuzz<Name>(f *testing.F)` functions in `_test.go` files in
the **same package** as the function under test (so unexported functions are reachable).
Each target:

1. Seeds the corpus with `f.Add(...)` using known-interesting inputs (valid cases,
   known past bugs, boundary values).
2. In `f.Fuzz(func(t *testing.T, ...) { ... })`, calls the target and asserts the
   relevant oracles (A always; B where an invariant exists).

A target **never** asserts on error *values* for arbitrary input (random input is
expected to error); it asserts on *invariants*: no panic, bounded output, and — where
applicable — the security/round-trip property holds whenever the function returns
success.

### Layer B — HTTP handler fuzzing (Approach 1)

A reusable harness under `backend/internal/server/fuzztest` (or a shared
`internal/testsupport` helper) provides:

- A `fake.NewSimpleClientset` / dynamic-fake-backed `ClientFactory` so handlers run
  without a real cluster. Remote-cluster paths are not exercised (no informers).
- A token-minting helper to produce valid/invalid/expired JWTs against a test
  `TokenManager`.
- A mountable router (the real `registerRoutes` chain, or per-handler sub-routers) so
  fuzzed requests pass through the genuine auth / CSRF / cluster-context / timeout
  middleware.
- Request builders that let the fuzzer control body bytes, selected headers
  (`Authorization`, `X-Requested-With`, `X-Cluster-ID`, `Impersonate-*`), and path/query
  params.

Each Layer B fuzz target builds a request, serves it through the harness, and asserts:

- **A:** handler does not panic; response is produced within the request timeout; no
  runaway allocation.
- **C:** if the request is unauthenticated/under-privileged, status is never 2xx on a
  protected route; if it mutates state without the CSRF header, it is rejected;
  client `Impersonate-*` headers never alter the effective identity.
- **D:** for handlers that return Secret-bearing payloads, the response body never
  contains a fuzz-injected sentinel "secret" value in cleartext — masking is intact.

> Note on impersonation oracle: k8sCenter uses *server-driven* impersonation derived
> from the authenticated identity. The oracle asserts client-supplied `Impersonate-*`
> headers cannot override it — a regression here would be a privilege-escalation bug.

## Target inventory (initial)

Confirmed to exist in the tree as of this design. Prioritized; the implementation plan
will batch these.

### Layer A — security-oracle targets (highest priority)

| Target | Package | Oracle |
|---|---|---|
| `EnforceNamespaces` / `parseMatchers` | `internal/loki` | Crash-safety + **on success, output query must contain only allowed namespace matchers** (injection / namespace-escape) |
| `ValidateRemoteURL` / `ValidateRemoteURLContext` | `internal/k8s` | Crash-safety + **any host resolving to private/loopback/link-local/CGNAT/metadata is rejected** (SSRF) |
| `validateSettingsURL` | `internal/server` | SSRF reject invariant |
| `validateVaultServerURL` | `internal/wizard` | SSRF reject invariant |
| `validateHTTPSPublicURL` | `internal/wizard` | SSRF + scheme invariant |
| `validateChannelURLs` | `internal/notifications` | SSRF reject invariant |
| `parsePHC` | `internal/auth` | Crash-safety on malformed Argon2id PHC strings (no panic on attacker-controlled stored hash) |

### Layer A — parser round-trip / invariant targets

| Target | Package | Oracle |
|---|---|---|
| `parseCompositeID` (gitops) | `internal/gitops` | Round-trip vs. ID formatter; no panic |
| `parseMeshCompositeID` | `internal/servicemesh` | Round-trip; no panic |
| `ParseRepoURL` | `internal/gitprovider` | No panic; success implies well-formed `RepoRef` |
| `ParseMultiDoc` | `internal/yaml` | No panic / no OOM on alias bombs, deep nesting, huge docs |
| `parseSelector` | `internal/k8s/resources` | No panic on malformed label selectors |
| `ParseThresholdAnnotations` | `internal/limits` | Invariant: malformed → documented defaults; `crit>=warn` fallback holds |
| `parseResourcePair` | `internal/wizard` | No panic on malformed quantity strings |

### Layer A — normalizer targets (idempotency / no-panic on arbitrary `unstructured`)

These take `*unstructured.Unstructured` (arbitrary nested maps coming from the k8s API)
and must never panic on unexpected shapes. Fuzz by generating arbitrary nested maps.

- `internal/servicemesh/normalize.go` — Istio + Linkerd normalizers
- `internal/gateway/normalize.go` — Gateway / HTTPRoute / Listener parsing
- `internal/gitops` — `NormalizeArgoApp`, `NormalizeFluxKustomization`, etc.
- `internal/policy` — `NormalizeKyvernoPolicy`, `NormalizeGatekeeperConstraint`
- `internal/velero/handler.go` — `parseBackup` / `parseRestore` / `parseSchedule` / `parseBSL` / `parseVSL`

### Layer B — HTTP handler targets (highest priority)

Drive fuzzed bodies/headers through the real middleware chain:

- `POST /auth/login`, `POST /auth/refresh` — malformed credentials/JSON; rate-limit and
  auth oracles.
- `POST /auth/oidc/{providerID}/mobile-exchange`, `.../mobile-config` — body-mode token
  exchange; no panic, no token leak on malformed input.
- `POST/PUT /settings` — SSRF-validated URL fields via the full handler.
- `POST/PUT /users` — privilege fields; authz oracle.
- Resource CRUD `decodeBody`-fed handlers — malformed JSON, oversized bodies (the
  `MaxBytesReader` path), unexpected types.
- A Secret-returning GET handler (e.g. secret detail) — **oracle D** masking check.

## Corpus & directory layout

- Generated corpus lives in Go's conventional `testdata/fuzz/<FuzzName>/` next to each
  test. Crash-reproducer files written there by `go test -fuzz` on failure are
  committed (they become permanent regression seeds).
- Seed inputs are added inline via `f.Add(...)` — no external seed files needed for the
  initial batches.
- The growing *coverage* corpus from the scheduled job is **not** committed in this
  phase (that is the deferred "Approach C — corpus persistence" upgrade). The scheduled
  job starts from seeds each run; if it gets noisy or slow, we graduate to
  `actions/cache`-backed persistence.

## CI integration

1. **PR gate (existing `ci.yml`):** `go test ./...` already runs every `Fuzz*` target's
   seed corpus as a normal test — no change required beyond the targets existing. The
   repo-wide `go vet ./... && go test ./...` check from CLAUDE.md Rule 4 covers them.
2. **Scheduled deep fuzz (new workflow, e.g. `.github/workflows/fuzz.yml`):**
   - Trigger: `schedule` (nightly or weekly) + `workflow_dispatch`.
   - For each fuzz target, run `go test -run=^$ -fuzz=^<Name>$ -fuzztime=<budget>`
     (budget per target, e.g. 3–10 min; total job time-boxed).
   - Enumerate targets via a small matrix or a generated list so new targets are picked
     up automatically.
   - On failure: the job fails, uploads the crash reproducer (`testdata/fuzz/...`) as an
     artifact, and (optionally) opens/updates a tracking issue. The reproducer is then
     committed as a regression seed and the bug fixed.

## Error handling & conventions

- Targets follow existing Go test conventions: same package for unexported targets,
  `t.Helper()` in shared assertions, table-free (fuzz drives the inputs).
- Oracles must avoid false positives: assert invariants only on the *success* branch;
  treat any returned `error` on random input as acceptable.
- No network, no real cluster, no Postgres in either layer. Layer B uses fakes only.
- Respect CLAUDE.md Rule 5 (sub-agent swarming) when authoring many targets across
  packages, and Rule 4 (repo-wide verification) before any push.

## Testing the tests

- Each fuzz target is committed with a seeded corpus that passes (`go test ./...`
  green).
- For at least the LogQL and SSRF targets, add a known-bad seed via `f.Add` that the
  oracle *catches if the guard is removed* — proving the oracle has teeth (a mutation
  check, not a live test).

## Out of scope (this phase)

- Layer C: full in-process server boot with Postgres/testcontainers + black-box route
  fuzzing.
- Coverage-corpus persistence across scheduled runs (`actions/cache` / committed corpus).
- Frontend (Deno) and mobile (Dart) fuzzing.
- Differential fuzzing against upstream parsers.

## Resolved planning decisions

1. **Cadence + budget:** Nightly `schedule` + `workflow_dispatch`. Each target runs
   `-fuzztime=5m` via a matrix so the job is parallel and time-boxed; nightly (not
   weekly) so a regression surfaces within a day.
2. **Layer B harness location:** New `internal/server/fuzztest` package holding the fake
   `ClientFactory`, token-minting helper, and request builders — keeps the harness out
   of production packages and reusable across handler targets.
3. **PR sequencing:**
   - **PR #1 (Layer A, security):** `loki.EnforceNamespaces`, the SSRF validators, and
     the two composite-ID parsers + the nightly `fuzz.yml` workflow. Smallest harness,
     highest security value. Includes the "oracle has teeth" mutation seeds.
   - **PR #2 (Layer B harness + first handlers):** `fuzztest` package + auth/login,
     settings (SSRF), and one Secret-returning GET (oracle D).
   - **PR #3 (Layer A breadth):** remaining parsers, `ParseMultiDoc`, `parsePHC`, and the
     `unstructured` normalizers.
