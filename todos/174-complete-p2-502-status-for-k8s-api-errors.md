---
status: pending
priority: p2
issue_id: "174"
tags: [code-review, pattern-consistency, step-11]
dependencies: []
---

# Rules CRUD Returns 502 BadGateway for K8s API Errors

## Problem Statement
All rules CRUD handlers return `http.StatusBadGateway` (502) for k8s API failures. 502 means "bad gateway" (proxy received invalid response from upstream). For direct client errors, 500 is more correct, or better yet, use `mapK8sError` to translate k8s status codes (404, 403, 409) to proper HTTP codes.

## Findings
- **Source**: Pattern Recognition review (Finding 2)
- **Location**: `backend/internal/alerting/handler.go:200,220,255,291,328`

## Proposed Solutions
### Option A: Use mapK8sError from resources package
Import and use the existing `mapK8sError` helper to translate k8s API errors to appropriate HTTP status codes.
- **Effort**: Small
- **Risk**: None

## Resources
- PR: #17
