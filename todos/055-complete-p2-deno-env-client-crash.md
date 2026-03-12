---
status: complete
priority: p2
issue_id: "055"
tags: [code-review, frontend, runtime]
dependencies: []
---

# Deno.env.get() in constants.ts Crashes in Browser

## Problem Statement
`frontend/lib/constants.ts` calls `Deno.env.get()` at module level. If this module is transitively imported by any island/client-side code, it will crash because `Deno` is not available in the browser. The `IS_BROWSER` guard from Fresh is not used.

## Findings
- `frontend/lib/constants.ts` — `Deno.env.get("BACKEND_URL")` at top level
- `frontend/lib/api.ts` imports from `constants.ts`
- Islands import from `api.ts` — but `api.ts` only uses `BACKEND_URL` which falls back to `/api`
- Currently works because the env var is undefined in browser and falls back, but `Deno.env` access itself may throw in strict browser environments

Flagged by: TypeScript Reviewer (P2), Security Sentinel (LOW)

## Proposed Solutions

### Option A: Guard with IS_BROWSER check
- **Pros**: Explicit, safe
- **Cons**: Slightly more code
- **Effort**: Small
- **Risk**: Low

```typescript
const BACKEND_URL = typeof Deno !== "undefined" ? Deno.env.get("BACKEND_URL") ?? "/api" : "/api";
```

## Technical Details
- **Affected files**: `frontend/lib/constants.ts`

## Acceptance Criteria
- [ ] constants.ts doesn't crash when imported in browser context
- [ ] Server-side still reads environment variables correctly
