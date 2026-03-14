---
status: pending
priority: p2
issue_id: "062"
tags: [code-review, frontend, architecture]
dependencies: []
---

# Namespace Selector State is Local to TopBar, Not Shared

## Problem Statement
The `selectedNs` signal in TopBar is local component state (`useSignal`). The selected namespace is the most critical shared state in a k8s management UI — every resource list, API call, and WebSocket subscription needs it. Step 5 (resource browser) will require a refactor if this isn't made shared now.

## Findings
- `frontend/islands/TopBar.tsx:14` — `const selectedNs = useSignal("all")` is local
- No shared namespace state module exists
- Dashboard also fetches namespaces independently (duplicate)

Flagged by: Architecture Strategist (P2-1), Pattern Recognition Specialist (P1-2)

## Proposed Solutions

### Option A: Extract to `lib/namespace.ts` with shared signal
- **Pros**: Cheap to do now, prevents Step 5 refactor
- **Cons**: None
- **Effort**: Small
- **Risk**: Low

Create `lib/namespace.ts` with `export const selectedNamespace = signal("all")`. TopBar writes to it, all consumers read from it.

## Technical Details
- **Affected files**: `frontend/islands/TopBar.tsx` (refactor out), new `frontend/lib/namespace.ts`

## Acceptance Criteria
- [ ] Namespace selection lives in shared module
- [ ] TopBar reads/writes the shared signal
- [ ] Other islands can import and react to namespace changes
