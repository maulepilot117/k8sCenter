---
status: pending
priority: p3
issue_id: "056"
tags: [code-review, frontend, typescript]
dependencies: []
---

# @ts-ignore for duplex in BFF Proxy

## Problem Statement
The BFF proxy uses `@ts-ignore` to suppress a TypeScript error for the `duplex: "half"` fetch option. This hides potential type issues and is a code smell.

## Findings
- `frontend/routes/api/[...path].ts:43-44` — `// @ts-ignore — Deno supports duplex for streaming` followed by `duplex: "half"`
- Deno's fetch supports `duplex` but the TypeScript types may not include it

Flagged by: TypeScript Reviewer (P1)

## Proposed Solutions

### Option A: Use @ts-expect-error instead
- **Pros**: Will alert if the type issue is fixed in a future Deno version
- **Cons**: Still suppresses error
- **Effort**: Small
- **Risk**: Low

### Option B: Type assertion on the options object
- **Pros**: More explicit
- **Cons**: More verbose
- **Effort**: Small
- **Risk**: Low

## Technical Details
- **Affected files**: `frontend/routes/api/[...path].ts`

## Acceptance Criteria
- [ ] No @ts-ignore in codebase
- [ ] Duplex streaming still works for request body forwarding
