---
status: pending
priority: p2
issue_id: "196"
tags: [code-review, performance]
dependencies: []
---

# Use Server-Side Pagination in List Handler

## Problem Statement

The list handler fetches ALL CiliumNetworkPolicies into memory before application-side pagination. The `metav1.ListOptions` supports `Limit` and `Continue` fields but they are not set. At 10,000+ policies, this causes ~40MB per request and API server pressure.

## Findings

- **cilium.go:97-109**: `ListOptions{LabelSelector: ...}` missing `Limit` and `Continue`
- Allocates `[]*unstructured.Unstructured` slice for all items, then paginates client-side
- 5-line fix: pass `Limit` and `Continue` from `params`, remove `paginate()` call
- Found by: Performance Oracle

## Proposed Solutions

### Option A: Pass Limit/Continue to ListOptions
```go
list, err = dc.Resource(ciliumPolicyGVR).Namespace(ns).List(r.Context(), metav1.ListOptions{
    LabelSelector: params.LabelSelector,
    Limit:         int64(params.Limit),
    Continue:      params.Continue,
})
```
Then return `list.Items` directly with `list.GetContinue()` token.
- Effort: Small
- Risk: Low (changes sort order from alphabetical to creation order)

## Acceptance Criteria
- [ ] List endpoint passes Limit/Continue to Kubernetes API
- [ ] Intermediate slice allocation removed
- [ ] Continue token forwarded to client

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- File: `backend/internal/k8s/resources/cilium.go:97-109`
