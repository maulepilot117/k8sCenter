---
status: pending
priority: p2
issue_id: "208"
tags: [code-review, typescript, type-safety]
dependencies: []
---

# Action handlers use `any` casts for resource.spec access

## Problem Statement

`action-handlers.ts` and `ResourceTable.tsx` use `deno-lint-ignore no-explicit-any`
3 times to access `resource.spec.replicas` and `resource.spec.suspend`. This
bypasses TypeScript's type checking and could mask bugs if the API shape changes.

**Locations:**
- `frontend/islands/ResourceTable.tsx:321` — `(resource as any).spec`
- `frontend/islands/ResourceTable.tsx:332` — `(resource as any).spec?.suspend`
- `frontend/islands/ResourceTable.tsx:631` — `(scaleTarget.value as any).spec?.replicas`
- `frontend/lib/action-handlers.ts:29` — `resource: any` parameter
- `frontend/lib/action-handlers.ts:90` — `params?.replicas as number` unsafe cast

## Findings

- K8sResource type in k8s-types.ts defines metadata but not spec (generic type)
- The action code needs to know about kind-specific spec fields (replicas, suspend)
- Current approach silences the compiler instead of properly narrowing types

## Proposed Solutions

### Option A: Add a WorkloadResource intersection type
- Extend K8sResource with optional `spec: { replicas?: number; suspend?: boolean }`
- **Pros:** Type-safe, simple
- **Cons:** Loose — all resources would have optional spec fields
- **Effort:** Small
- **Risk:** Low

### Option B: Type guard functions per kind
- `isScalable(r): r is K8sResource & { spec: { replicas: number } }`
- **Pros:** Precise typing
- **Cons:** More boilerplate
- **Effort:** Medium
- **Risk:** Low

## Recommended Action

Option A — pragmatic and sufficient for the action menu use case.

## Acceptance Criteria

- [ ] No `deno-lint-ignore no-explicit-any` in action-related code
- [ ] `params?.replicas` has proper type narrowing
- [ ] `deno lint` passes without suppressions

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found during PR #46 TypeScript review |
