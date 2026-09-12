---
title: Backend resilience conventions — fuzzing and goroutine panic safety
date: 2026-06-27
last_updated: 2026-09-12
category: docs/solutions
module: backend/internal
problem_type: convention
component: service_layer
severity: high
related_components: [testing_framework, infrastructure]
tags: [fuzzing, goroutine-safety, panic-recovery, recoverutil, crd-normalizers, ci-drift-guard, backend-resilience]
applies_when:
  - "Adding a background goroutine, errgroup worker, or ticker loop to the Go backend"
  - "Adding a fuzz target for a parser at a security or reliability seam"
  - "Adding or renaming a row in the nightly fuzz.yml matrix"
  - "Reviewing whether a panic on a non-request stack can crash the process"
---

# Backend Resilience Conventions — Fuzzing & Goroutine Panic Safety

**Status:** active convention. Consolidates patterns established across PRs #370–#377
(see *History* at the bottom). Read this before adding a new background goroutine,
errgroup fan-out, or fuzz target to the Go backend.

Two adjacent risk classes, one root cause: **adversarial cluster data reaching code
that wasn't written to expect it.** A malformed CRD, a hostile LogQL filter, or a
wrong-typed `unstructured` field can panic a parser. This document defines how we
(1) *find* those panics ahead of time (fuzzing) and (2) *contain* the ones that slip
through (goroutine panic recovery).

---

## Part 1 — Goroutine panic safety (`internal/recoverutil`)

### The rule

**Every goroutine that runs OUTSIDE chi's HTTP panic-recovery middleware MUST wrap
its work with a `recoverutil` helper.** chi's `Recoverer` only protects the request
goroutine; a panic on any *other* goroutine stack is unrecovered and **crashes the
whole process** (`go func(){...}` fan-outs, `errgroup` workers, `sync.WaitGroup.Go`
bodies, background ticker loops, fire-and-forget `go svc.Method(...)`).

This is not theoretical: a normalizer reading `obj["metadata"].(map[string]any)`
without the comma-ok form panics on a CRD with no/`wrong-typed` metadata — and if
that normalizer runs in a poller goroutine or an errgroup worker, the panic is fatal
to the server, not a graceful 500.

### Pick the wrapper by goroutine shape

| Shape | Helper | Behavior |
|-------|--------|----------|
| `errgroup` worker (`g.Go(func() error {...})`) | `recoverutil.Go(g, logger, label, fn)` | recover → log (with stack) → convert panic to a returned error so `g.Wait()` surfaces it as an ordinary failure |
| Background ticker-loop body | `recoverutil.Tick(ctx, logger, label, fn)` | recover → log → the caller's loop continues to its next tick |
| Plain `go func(){...}` / fire-and-forget `go svc.M(...)` / `wg.Go(...)` body | `recoverutil.Safe(logger, label, fn)` | recover → log |

All three log under a stable **`task`** key (so one structured-log filter,
`task=<label>`, catches every recoverutil panic) and include a `debug.Stack()` so the
offending line is traceable. A `nil` logger falls back to `slog.Default()` — a
recovered panic is **never** silently dropped.

### The channel-send / cleanup hazard (read this — it's the easy way to break things)

`recoverutil.Safe`/`Go` swallow the panic *inside* the wrapped `fn`. Anything that
**must run regardless of panic** has to live OUTSIDE the wrapped closure:

- **`defer wg.Done()`** — register it on the goroutine BEFORE calling `Safe`, never
  inside `fn`. Otherwise a panic that `Safe` recovers would still have skipped
  `wg.Done()` had it been inside — and `wg.Wait()` hangs forever.
- **Channel sends a reader counts.** If a reader does `for i := 0; i < N; i++ { <-ch }`
  or `<-ch` once, every spawned goroutine MUST send exactly once. A panic inside `fn`
  that swallows the send → the reader deadlocks. Keep the send outside `Safe`, or
  defer a guaranteed zero-value send and let `fn` populate the value by reference:

  ```go
  // CORRECT: wg.Done outside; send guaranteed; only the parse is recovered.
  go func(i int) {
      defer wg.Done()
      out := result{name: name} // zero value sent even on panic
      defer func() { outcomes <- out }()
      recoverutil.Safe(logger, "pkg task", func() {
          out = parse(adversarialData) // panic here → out stays zero → reader skips it
      })
  }(i)
  ```

- **Semaphore release / mutex unlock** registered as a `defer` inside `fn` IS safe —
  it runs during `fn`'s unwind before `Safe`'s `recover` fires. But if the acquire is
  meant to bound goroutine *creation*, keep it where it was (don't move it inside the
  goroutine and change the concurrency profile).

### Labels

Package-prefix every label for greppability: `"certmanager list certificates"`,
`"gateway resolve-parent-ref"`, `"externalsecrets poller tick"`. Disambiguate
otherwise-identical call sites (e.g. local vs remote cluster: `"certmanager list
certificates"` vs `"certmanager remote list certificates"`).

### Sanctioned exceptions (do NOT force recoverutil here)

- **Deliberately-silent per-item sibling isolation.** A `defer func(){ _ = recover() }()`
  inside a per-item dispatch goroutine whose *outer* loop already has a logged recover
  (e.g. `externalsecrets` `dispatchEmits` / `persistAttempts`) — logging every item
  failure would be noise; the cycle-level recover carries the signal. Leave them;
  add a comment noting the intent.
- **Domain-aware recovery** that must write state on panic (e.g. `externalsecrets`
  `BulkWorker.run` writes a `worker_panic` outcome to the DB so the UI poll
  terminates). A generic helper can't express that — keep it hand-rolled.

### Adding a new goroutine — checklist

1. Does it run outside chi middleware? (background loop, errgroup, `go func`, `wg.Go`)
   → yes: it needs a wrapper.
2. Pick `Go` / `Tick` / `Safe` by shape (table above).
3. Move `wg.Done()` / counted channel sends / cleanup that must always run OUTSIDE the
   wrapped closure.
4. Package-prefix the label.
5. The wrappers are unit-tested centrally (`internal/recoverutil`); you don't need a
   per-site panic test — but if the call site has non-trivial channel/waitgroup
   choreography, add a test that panics the body and asserts no deadlock.

---

## Part 2 — Native Go fuzzing

### What to fuzz

The highest-value targets are **pure functions at a security/reliability seam that
parse attacker-influenceable input**: CRD normalizers (`unstructured` → domain
struct), auth/PHC parsing, SSRF IP/URL classification, LogQL/label enforcement, YAML
multi-doc parsing, secret-masking. If a function turns bytes/`unstructured`/a string
from outside the trust boundary into a typed value, it's a candidate.

### Oracle taxonomy

| Oracle | Question | Example targets |
|--------|----------|-----------------|
| **A — crash-safety** | "never panics on any input" | CRD normalizers, `ParseMultiDoc`, `parsePHC` |
| **B — parser invariants / round-trip** | "parse∘format is identity; output always well-formed" | composite-ID parse/format |
| **C — auth/authz enforcement** | "no input bypasses the guard" | `FuzzAuthzEnforcement` (CSRF/auth middleware) |
| **D — secret-leak masking** | "secret values never survive in output" | `FuzzMaskedSecret` |

Normalizers are **Oracle A only** by design — they aren't invertible, so round-trip
doesn't apply.

### Conventions (non-negotiable)

- **In-package placement.** Fuzz files live as `*_fuzz_test.go` in the SAME package as
  the target (`package certmanager`, not `package certmanager_test`) so they can reach
  unexported functions. Name the file for its target (`normalize_fuzz_test.go`;
  `parsers_fuzz_test.go` when the targets are `parseX` in `handler.go`).
- **Oracle teeth via mutation seeds.** A seed corpus that passes even against the buggy
  code is hollow. Include seeds that WOULD fail if a production guard were removed —
  and verify by mutation (temporarily delete the guard; the seed must panic/fail).
  For normalizers: `{}` (no metadata), `{"metadata":"oops"}` (wrong type),
  `{"spec":[],"status":"x"}` cover the common type-assertion panics.
- **Independent re-derivation of constants.** When the oracle checks a guard whose own
  constant could drift (e.g. the CGNAT `100.64.0.0/10` block), re-derive it in the
  test independently rather than referencing the production var — so the oracle detects
  the guard being removed instead of silently agreeing with it. (Exception: an oracle
  whose job is to *audit* the guard reuses the production value.)
- **Shared `unstructured` generator.** For CRD-normalizer crash-safety, decode fuzz
  bytes into an `*unstructured.Unstructured` and dispatch through every normalizer in
  the package. The helper is duplicated per package (Go test files can't share
  unexported helpers cross-package; a real exported package would ship dead code in the
  binary) — this duplication is accepted:

  ```go
  func unstructuredFromFuzz(data []byte) (*unstructured.Unstructured, bool) {
      var m map[string]any
      if err := yaml.Unmarshal(data, &m); err != nil || m == nil {
          return nil, false
      }
      return &unstructured.Unstructured{Object: m}, true
  }
  ```

- **Hermetic.** Fuzz targets must not touch the network, PostgreSQL, or a real cluster.
  Fuzz pure functions / fakes only. (This is why some "SSRF validators" that re-parse a
  URL and do real DNS are NOT fuzzed directly — fuzz the pure classifier they delegate
  to instead.)

### CI wiring (`.github/workflows/fuzz.yml`)

- One nightly matrix row per target: `{ pkg: ./internal/<pkg>/, target: Fuzz<Name> }`.
- **`-list` drift guard** on every row: `go test $PKG -list '^$TARGET$' | grep -qx
  '$TARGET'`. `go test -fuzz=^XYZ$` exits 0 on a no-match regex, silently dropping
  coverage — the guard fails fast if a target is renamed/removed without updating the
  matrix.
- **SHA-pinned actions**, reusing the already-vetted SHAs from `ci.yml`/`e2e.yml` (per
  the 7-day supply-chain cooldown — never resolve a fresh `@vN` tag here).
- `-fuzztime=5m` per target; crash reproducers uploaded as artifacts on failure;
  `backend/**/testdata/fuzz/` is gitignored except committed regression seeds.

### Adding a new fuzz target — checklist

1. In-package `*_fuzz_test.go`, `Fuzz<Name>` func.
2. Seed corpus: realistic valid inputs + malformed/adversarial "teeth" that reproduce
   the failure class. Verify teeth by mutation.
3. Add a matrix row to `fuzz.yml` (the `-list` guard covers it automatically).
4. Run locally: `go test ./internal/<pkg>/ -run=^$ -fuzz=Fuzz<Name> -fuzztime=15s`.

---

## History

| PR | Arc | What |
|----|-----|------|
| #370 | fuzz | Layer A security parsing seams + nightly deep-fuzz CI |
| #371 | fuzz | Layer B auth/authz (oracle C) + secret-masking (oracle D) |
| #372 | fuzz | parsePHC, ParseMultiDoc, isPublicIP (Layer A breadth) |
| #373 | fuzz | CRD-normalizer crash-safety across 7 packages; **found + fixed 2 real certmanager panics** |
| #374 | recovery | certmanager poller + errgroup panic recovery (ESO parity) |
| #375 | recovery | extracted `internal/recoverutil` (Go/Tick/Safe) + swept the gap across the backend |
| #377 | fuzz | closed the arc: `ClientFactory` → `kubernetes.Interface` seam + `FuzzSecretPipeline` (`backend/internal/k8s/resources/secret_pipeline_fuzz_test.go`) — full HTTP-handler Secret fuzzing under oracles A and D |

(#376 is this document itself, so it is not listed as a source.)

Design rationale: `docs/superpowers/specs/2026-06-25-backend-fuzzing-design.md`.

## See Also

- `docs/solutions/postgres-test-harness-conventions.md` — the env-gated PostgreSQL
  integration-test harness. **Scope note for the Hermetic rule above:** that rule governs
  *fuzz targets* only, which is why it names them as its subject. PostgreSQL-backed
  `Test*` functions gated on `KUBECENTER_TEST_DATABASE_URL` are a separate and sanctioned
  category that deliberately touches a real database; they do not violate it. Fuzzing
  needs pure functions because it runs millions of iterations, not because touching
  Postgres is unsafe in general.
- `docs/solutions/yaml-rate-limiter-e2e-flake.md` — same family as the `-list` drift
  guard above: a CI signal that reports success while proving nothing. There the guard
  was a no-match fuzz regex exiting 0; there it was a green e2e run whose assertion could
  never have been satisfied.
