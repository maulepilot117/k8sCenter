---
title: The wizard e2e "flake" that was a per-IP rate limit
date: 2026-09-12
category: docs/solutions
module: backend/cmd/kubecenter
problem_type: test_failure
component: api_layer
symptoms:
  - "e2e/tests/wizard-flows.spec.ts failed on exactly 1 of 99 tests per run, with the failing wizard rotating between runs"
  - "Playwright reported only a bare visibility timeout on the Apply button: \"element(s) not found\", naming no cause"
  - "All three attempts (initial plus retries: 2) failed identically rather than one flaking and the retry passing"
  - "The page snapshot showed \"Step 3 of 3\" with \"Failed to generate preview\" and \"API error 429: rate limit exceeded\""
  - "Raising the frontend timeout from 5s to 10s changed nothing"
root_cause: config_error
resolution_type: code_fix
severity: medium
related_components: [testing_framework, infrastructure]
tags: [rate-limiting, e2e-tests, playwright, flaky-test, wizards, dev-environment, misdiagnosis]
framework_version: "Go 1.26 / Playwright (Node.js)"
---

# The wizard e2e "flake" that was a per-IP rate limit

**Status:** active learning. Fixed in PR #420. Read this before raising a timeout in
`e2e/`, and before adding a `cfg.Dev` branch to any limiter in
`backend/cmd/kubecenter/main.go`.

## Problem

One Playwright wizard test out of 99 failed intermittently for roughly two weeks, with
the failing wizard rotating between runs. The failure was not a UI race: the backend's
shared YAML/wizard rate limiter had no dev relaxation, so the e2e suite's own traffic —
all of it from one runner IP — exhausted a production-sized per-IP bucket and the wizard
preview came back `429`.

## Symptoms

- `e2e/tests/wizard-flows.spec.ts` failed on exactly 1 of 99 tests per run; which wizard
  failed varied run to run (NetworkPolicy, then ConfigMap next time).
- The Playwright error was a bare visibility timeout on the Apply button — "element(s)
  not found" — naming no cause.
- All three attempts (initial + `retries: 2`, `e2e/playwright.config.ts:9`) failed
  identically rather than one flaking and the retry passing.
- The real error was only visible in the failing run's `error-context.md` page snapshot,
  which the test itself discarded:

```
Create Network Policy  Step 3 of 3 · namespace default
paragraph: Failed to generate preview
paragraph: "API error 429: rate limit exceeded"
```

That string is the limiter's own rejection body — `Message: "rate limit exceeded"` at
`backend/internal/server/middleware/ratelimit.go:166`, sent with `Code: 429`
(`ratelimit.go:165`).

## What Didn't Work

**The hydration-race theory.** The snapshot header reads `Step 3 of 3` — a 3-step wizard
that had already stepped to its review step. The island had hydrated, every Next click
had landed, the form state had survived each step swap. Every symptom the theory was
invented to explain (intermittent, rotating victim, times out at the last step) is
equally explained by a shared token bucket that happens to empty at a different point in
the suite each run. The theory was never tested against the page snapshot, which
contradicts it outright.

**The 5s -> 10s timeout bump.** This could not have worked, and the reason is structural
rather than a matter of degree. `WizardReviewStep` returns early from two branches before
it ever reaches the Apply button:

- `frontend/components/wizard/WizardReviewStep.tsx:80-89` — `if (loading)` returns a
  spinner and "Generating YAML preview...".
- `frontend/components/wizard/WizardReviewStep.tsx:91-98` — `if (error)` returns the
  danger panel containing "Failed to generate preview" and the error text.

The Apply button renders only in the final fall-through return, at
`WizardReviewStep.tsx:224-233`. So Apply exists only when the preview has both
**finished** and **succeeded**. On a 429 the component sits permanently in the `error`
branch. There is no Apply button in the DOM at t=5s, t=10s, or t=infinity — the wait is
not slow, it is unsatisfiable. Raising the timeout only lengthened the interval before
the same "not found" message, and Playwright's retries re-hit the same still-empty per-IP
bucket, which is why all attempts failed together instead of the retry rescuing the run.

Generalizing: a timeout bump is only ever a valid fix when the awaited element
*eventually appears*. Before raising one, establish that the element can appear at all.

**This was the second time.** PR #238 (2026-05-07) fixed an earlier flake in *this same
file*, also attributed to frontend timing (a "step-transition race" between button
unmount and mount), and also addressed in part by extending a wait from 5s to 10s. Two
separate investigations of `wizard-flows.spec.ts` reached for a frontend timing
explanation and a longer timeout. The recurrence is the strongest argument for rule 3
below: the artifact that names the real cause was available both times.

## Solution

The auth limiter already had a dev relaxation; the YAML/wizard limiter did not.
`backend/cmd/kubecenter/main.go:291-296` builds `rateLimiter` with an explicit `cfg.Dev`
branch (`NewRateLimiterWithRate(60, time.Minute) // relaxed for dev` vs.
`NewRateLimiter() // 5 req/min for production`). The line immediately below it hardcoded
its budget in every environment:

```go
// before — main.go, same block as the dev-aware auth limiter
yamlRateLimiter := middleware.NewRateLimiterWithRate(30, time.Minute)
```

PR #420 extracted the choice into a testable helper and passed `cfg.Dev` through:

```go
// after — backend/cmd/kubecenter/main.go:299
yamlRateLimiter := middleware.NewRateLimiterWithRate(yamlRateLimit(cfg.Dev))
```

```go
// backend/cmd/kubecenter/ratelimits.go:25-30
func yamlRateLimit(dev bool) (int, time.Duration) {
	if dev {
		return 300, time.Minute
	}
	return 30, time.Minute
}
```

300/min is not a new shape — `webhookRateLimiter` already uses
`NewRateLimiterWithRate(300, time.Minute)` at `backend/cmd/kubecenter/main.go:422`.
Production stays at 30/min.

`backend/cmd/kubecenter/ratelimits_test.go` guards both halves: dev must be at least
`minimumDevBudget = 100` (`ratelimits_test.go:26,33-36`), prod must remain exactly 30
(`ratelimits_test.go:42-45`), and dev must exceed prod (`ratelimits_test.go:47-49`). The
test was written first and observed failing with `undefined: yamlRateLimit` before the
helper existed.

The e2e assertion was rewritten to race the known failure element against the success
element (`e2e/tests/wizard-flows.spec.ts:129-132`):

```ts
const previewFailed = page.getByText(/Failed to generate preview/i);
await expect(submitButton.or(previewFailed).first()).toBeVisible({ timeout: 15_000 });
```

and, when the error branch wins, to read the panel's text and throw with it
(`wizard-flows.spec.ts:133-143`), so the next failure reports
`API error 429: rate limit exceeded` instead of "element(s) not found". Its comment now
states outright that raising the timeout cannot help (`wizard-flows.spec.ts:114-128`).

## Why This Works

`backend/internal/server/routes.go` wires **16** route groups through the single shared
`s.YAMLRateLimiter`, so in a default build they all draw from one bucket: wizards,
`/yaml/*`, secrets, and the rest. Fourteen of them bind it as `yamlRL :=` (`routes.go:290,
307, 406, 428, 453, 474, 501, 539, 553, 570, 623, 637, 651, 695`); Gateway
(`routes.go:782`) and Service Mesh (`routes.go:806`) bind the same limiter as `rl :=`.
Count by the field, `s.YAMLRateLimiter`, not by the local variable name — grepping the
`yamlRL :=` spelling alone undercounts by two, which is exactly the error the first
version of this document made. Every group falls back to the auth limiter when
`YAMLRateLimiter` is nil (e.g. `routes.go:291-293`).

That bucket is keyed per client IP — `extractIP` splits `r.RemoteAddr`
(`backend/internal/server/middleware/ratelimit.go:139`), which chi's RealIP middleware
has already rewritten from `X-Real-IP` / `X-Forwarded-For` (`ratelimit.go:136-137`). The
e2e suite presents as exactly one such client: `e2e/playwright.config.ts:10` sets
`workers: process.env.CI ? 1 : undefined` and `fullyParallel: false`
(`playwright.config.ts:7`), so 99 tests run serially from one machine, hitting all 16
guarded groups within a few minutes. Against a 30/min budget that is indistinguishable
from an abusive single source, and the bucket empties. Whichever wizard's preview
happened to land after exhaustion was the one that "flaked" — hence the rotating victim.

The e2e backend already runs with `KUBECENTER_DEV: "true"`
(`e2e/playwright.config.ts:40`), so the dev branch that the auth limiter had been using
all along was available to the YAML limiter too; it simply was never wired. Passing
`cfg.Dev` in closes the asymmetry. Dev is not a security boundary here — the production
budget is untouched and test-guarded.

Worth knowing: this bucket exists *because* it was once too tight. Issue #10 ("YAML
endpoints share rate limit bucket with auth - unusable in practice") led to PR #13, which
introduced `NewRateLimiterWithRate` and split the 30/min YAML bucket out of the 5/min
auth bucket. The same endpoints have now been squeezed twice by the limiter guarding
them; treat their budget as a known pressure point rather than a settled number.

## Prevention

1. **A dev-mode relaxation must cover every limiter, not just the obvious one.** The
   defect was not the 30/min number; it was that one limiter in a block of sibling
   limiters was dev-aware and its neighbour was not. When adding a `cfg.Dev` branch to any
   limiter, quota, or timeout, grep for every other constructor of the same type in that
   file and decide explicitly for each. In this repo that means auditing all four:
   `rateLimiter`, `yamlRateLimiter`, `logQueryLimiter` (`main.go:323`),
   `webhookRateLimiter` (`main.go:422`).

2. **A test suite sharing one IP is indistinguishable from an abusive client to a per-IP
   limiter.** Any serial e2e suite (`workers: 1`, `fullyParallel: false`) is a single
   high-rate client by construction, and retries replay the same traffic into the same
   still-empty bucket — which is exactly why all attempts fail together rather than the
   retry passing. When you add a per-IP limiter to a route the e2e suite touches, size the
   dev budget against the *whole suite's* burst, including retries, not a single test.

3. **Read the Playwright `error-context.md` artifact before touching a timeout.** The page
   snapshot recorded the literal string `API error 429: rate limit exceeded` from the
   first failing run onward. The application rendered the real cause on screen; the test's
   `toBeVisible` wait discarded it and reported "element(s) not found". Two weeks of wrong
   theory sat on top of an artifact that named the bug — and per PR #238 above, this is
   the second investigation of this file to skip that artifact.

4. **An assertion that waits for a success-only element should race the known error
   element.** If a component's error branch returns early without the element under test,
   waiting for that element can never distinguish "slow" from "failed" — it always reports
   a timeout. Assert `success.or(knownError).first()`, then branch: if the error won, read
   its text and throw with it. The failure then names its own cause. Worth doing wherever
   a UI has an `if (error) return ...` branch that omits the primary action.

5. **When a "flaky" test's victim rotates between runs, suspect a shared, exhaustible
   resource** — a rate-limit bucket, a connection pool, a quota, a fixture — before
   suspecting per-test timing. Per-test races tend to pin on the same test; a shared
   budget moves the failure to whoever arrives after it runs out.

## Verified Outcome

Three consecutive clean e2e attempts on the fix (run 34703065287, attempts 1-3, all
`success`): 223 passed, 0 failed, 0 flaky, zero
occurrences of "rate limit exceeded". Backend `go vet ./...` and `go test ./...` clean
across 33 packages; the guard test fails before the helper exists
(`undefined: yamlRateLimit`) and passes after.

## Related Issues

- **PR #420** — the fix this document describes.
- **PR #238** (2026-05-07) — earlier `wizard-flows.spec.ts` flake, also diagnosed as
  frontend timing, also partly addressed with a 5s -> 10s wait. The prior instance of the
  anti-pattern in rule 3.
- **Issue #10 / PR #13** (2026-03-13) — why a separate YAML bucket exists at all; the
  endpoints were "unusable in practice" sharing the auth bucket.
- **Issue #276 / PR #294** — adjacent work in the same limiter: 429s now reach the audit
  table. Useful if you need to see limiter rejections after the fact rather than from a
  page snapshot.

## See Also

- `docs/solutions/backend-resilience-conventions.md` — different subject matter
  (goroutine panic safety, CRD fuzzing), but its `-list` drift guard is the same family
  of lesson: a CI signal that reports success while proving nothing. There the fix was a
  hard-failing existence assertion; here it was making the limiter's dev/prod behaviour an
  explicit, independently testable function.

## History

- **2026-09-12** — documented from PR #420 (`fix/e2e-wizard-preview-rate-limit`).
