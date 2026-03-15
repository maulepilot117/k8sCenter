---
status: complete
priority: p3
issue_id: "181"
tags: [code-review, pattern-consistency, frontend, step-11]
dependencies: []
---

# AlertEvent Interface Duplicated Across Frontend Islands

## Problem Statement
`AlertEvent` is defined in both `AlertsPage.tsx` (full, 14 fields) and `AlertBanner.tsx` (subset, 3 fields). Shared types should be in `lib/k8s-types.ts` per existing pattern.

## Proposed Solutions
Move `AlertEvent` to `lib/k8s-types.ts` and import from both islands.
- **Effort**: Small

## Resources
- PR: #17
