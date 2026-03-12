---
status: complete
priority: p2
issue_id: "048"
tags: [code-review, frontend, simplicity, yagni]
dependencies: []
---

# Remove ~519 Lines of Unused/YAGNI Code

## Problem Statement
The frontend skeleton includes approximately 519 lines of code that are either completely unused or implement features not needed until later steps. This adds maintenance burden, increases bundle size, and creates confusion about what's actually functional.

## Findings
- `frontend/lib/ws.ts` (~167 LOC) — Full WebSocket client with reconnection. Step 5 deliverable, completely unused. Also has bugs: infinite reconnect without max retries, stale token on reconnect, listener dispatch drops wildcard events.
- `frontend/lib/formatters.ts` (~80 LOC) — formatAge, formatBytes, etc. No imports anywhere.
- `frontend/components/ui/Toast.tsx` (~60 LOC) — `showToast()` never called anywhere.
- `frontend/components/ui/Badge.tsx` — Unused component, no imports.
- `frontend/components/ui/StatusBadge.tsx` — Unused component, no imports.
- `frontend/components/layout/PageHeader.tsx` — Unused component, no imports.
- `frontend/lib/k8s-types.ts` — 9 of 12 interfaces unused (only `UserInfo`, `ApiResponse`, `K8sResource` are used). `UserInfo` doesn't match backend shape.

Flagged by: Simplicity Reviewer (Primary), TypeScript Reviewer (P3)

## Proposed Solutions

### Option A: Delete all unused files and interfaces now
- **Pros**: Clean codebase, no dead code, smaller bundle
- **Cons**: Need to re-create when actually needed
- **Effort**: Small
- **Risk**: Low (they can be recreated from git history)

### Option B: Keep but gate behind a TODO comment
- **Pros**: Easier to re-enable
- **Cons**: Still clutters codebase
- **Effort**: Small
- **Risk**: Low

## Technical Details
- **Files to delete**: `ws.ts`, `formatters.ts`, `Toast.tsx`, `Badge.tsx`, `StatusBadge.tsx`, `PageHeader.tsx`
- **Files to trim**: `k8s-types.ts` (remove unused interfaces)

## Acceptance Criteria
- [ ] No unused files in the frontend tree
- [ ] No unused exports in remaining files
- [ ] Frontend still builds and lints cleanly after removal
