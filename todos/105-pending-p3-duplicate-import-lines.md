---
status: pending
priority: p3
issue_id: "105"
tags: [code-review, quality, step-6]
dependencies: []
---

# Consolidate Duplicate Import Lines in Overview Components

## Problem Statement
All 18 overview components import from `@/lib/k8s-types.ts` on two separate lines:
```typescript
import type { K8sResource } from "@/lib/k8s-types.ts";
import type { Deployment } from "@/lib/k8s-types.ts";
```
Should be a single import: `import type { K8sResource, Deployment } from "@/lib/k8s-types.ts";`

## Findings
- **Agent**: pattern-recognition-specialist (PR #6 review)
- **Location**: All 18 `*Overview.tsx` files in `frontend/components/k8s/detail/`

## Proposed Solutions
Consolidate into single import statements across all 18 files.
- **Effort**: Small
- **Risk**: None

## Acceptance Criteria
- [ ] Each overview file imports from `@/lib/k8s-types.ts` exactly once
