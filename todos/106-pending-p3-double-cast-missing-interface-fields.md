---
status: pending
priority: p3
issue_id: "106"
tags: [code-review, quality, step-6]
dependencies: []
---

# Add Missing Fields to TypeScript Interfaces (Eliminate Double-Casts)

## Problem Statement
Several overview components use a double-cast pattern to access fields not defined in the TypeScript interfaces:
```typescript
const strategy = (spec as Record<string, unknown>).strategy as { type?: string; ... } | undefined;
```
This indicates missing fields in the `Deployment`, `StatefulSet`, `PVC`, and `CronJob` interfaces in `k8s-types.ts`.

## Findings
- **Agent**: pattern-recognition-specialist (PR #6 review)
- **Location**: `DeploymentOverview.tsx:12` (strategy), `StatefulSetOverview.tsx:11` (updateStrategy), `PVCOverview.tsx:12` (volumeName), `CronJobOverview.tsx:12` (concurrencyPolicy)

## Proposed Solutions
Add the missing fields to the TypeScript interfaces in `frontend/lib/k8s-types.ts` so the overview components can access them directly without double-casting.
- **Effort**: Small
- **Risk**: None

## Acceptance Criteria
- [ ] No `Record<string, unknown>` intermediate casts in overview components
- [ ] All accessed fields defined in their respective TypeScript interfaces
