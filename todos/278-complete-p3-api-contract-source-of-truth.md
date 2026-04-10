---
status: complete
priority: p3
issue_id: 278
tags: [architecture, debt, code-review, pr-163]
dependencies: []
---

## Problem Statement

PR #163's root cause was that the frontend called `/v1/policy/*` while the backend mounted at `/v1/policies/*`. The mismatch shipped in Phase 8B (#138) and went undetected for months because there is no contract between `backend/internal/server/routes.go` and `frontend/lib/api.ts` — just string literals on both sides. Any future route mount-point change is a latent silent-404 bug.

## Findings

- `routes.go`: routes registered with `chi.Route(...)` via string literals.
- `frontend/lib/api.ts` and islands: call `apiGet("/v1/...")` with string literals.
- No E2E test hits the routes that broke. Existing Playwright `e2e/` suite does not currently open `/security/policies`.
- This is a systemic risk: every integration with a dedicated page (policies, gitops, investigate, etc.) has the same shape of mismatch waiting.

## Proposed Solutions

### Option A — Route manifest + Playwright smoke test
Single source of truth file (`routes.json` or generated from chi) listing every API path. A Playwright test logs in and GETs each path, asserting non-404. Cheapest durable fix.
- Effort: Small.
- Catches: URL typos, accidental route deletions, path renames.
- Misses: Schema drift in the response body.

### Option B — OpenAPI spec + generated TS client
Hand-maintained or emitted (e.g. via `huma`) OpenAPI; TS client generated with `openapi-typescript`.
- Effort: Medium–Large.
- Catches: URL mismatches, request/response shape drift, type errors at compile time.
- Misses: Nothing for HTTP surface.

### Option C — Lint rule
ESLint rule forbidding inline API path strings outside `lib/api.ts`. Forces centralization but doesn't verify backend alignment.
- Effort: Small.
- Catches: Nothing — it just nudges.

## Recommended Action

## Technical Details

**Lightest useful fix (Option A):**
- Add `e2e/tests/api-routes.spec.ts` that reads `e2e/fixtures/routes.json` and hits every path as an authenticated admin, asserting `!== 404`.
- Add `make check-routes` target and wire into CI.
- Manually maintain the fixture until/unless Option B is adopted.

## Acceptance Criteria

- [ ] A test that would have failed on the `/v1/policy/status` mismatch introduced in #138.
- [ ] Runs in CI on every PR.

## Work Log

_2026-04-10_: Identified during `/review` on PR #163 by architecture-strategist.

## Resources

- maulepilot117/k8sCenter#163 — the bug this would have caught.
- `backend/internal/server/routes.go`
- `frontend/lib/api.ts`
