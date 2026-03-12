---
status: pending
priority: p3
issue_id: "057"
tags: [code-review, frontend, typescript]
dependencies: []
---

# Dead SVGContent Function in ResourceIcon.tsx

## Problem Statement
`ResourceIcon.tsx` contains a `SVGContent` function at line 170 that is never called. It exists only as a type helper for `ReturnType<typeof SVGContent>`, but its type signature `(props: Record<string, never>)` returning `null` doesn't actually produce the correct type for the icon values.

## Findings
- `frontend/components/k8s/ResourceIcon.tsx:170-172` — Dead function
- The `icons` record type `Record<string, ReturnType<typeof SVGContent>>` resolves to `Record<string, null>` which is incorrect — the values are JSX elements, not null

Flagged by: TypeScript Reviewer (P3)

## Proposed Solutions

### Option A: Remove function, use proper JSX type
- **Pros**: Correct types, no dead code
- **Cons**: None
- **Effort**: Small
- **Risk**: Low

Use `Record<string, preact.JSX.Element>` or `Record<string, ComponentChildren>` instead.

## Technical Details
- **Affected files**: `frontend/components/k8s/ResourceIcon.tsx`

## Acceptance Criteria
- [ ] SVGContent function removed
- [ ] Icons record has correct type
- [ ] No type errors
