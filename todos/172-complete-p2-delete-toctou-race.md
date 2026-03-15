---
status: pending
priority: p2
issue_id: "172"
tags: [code-review, data-integrity, step-11]
dependencies: []
---

# PrometheusRule Delete() Has TOCTOU Race

## Problem Statement
`Delete()` performs GET (to check managed-by label) then DELETE as two separate API calls. Between them, the resource could be modified or deleted by another user, or the label could be removed.

## Findings
- **Source**: Data Integrity review (P2-5)
- **Location**: `backend/internal/alerting/rules.go:254-265`

## Proposed Solutions
### Option A: Pass ResourceVersion precondition in DeleteOptions
Use `Preconditions{ResourceVersion: obj.GetResourceVersion()}` so the API server rejects the delete if the resource changed.
- **Effort**: Small
- **Risk**: None

## Resources
- PR: #17
