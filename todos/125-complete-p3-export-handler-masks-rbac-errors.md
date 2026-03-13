---
status: pending
priority: p3
issue_id: "125"
tags: [code-review, quality, step-7]
dependencies: []
---

# Export Handler Masks RBAC Errors as 404

## Problem Statement

The export handler returns 404 for all k8s API GET errors. If the API returns 403 (forbidden), the user sees "not found" instead of a permission error, making RBAC issues difficult to diagnose.

## Findings

- **File:** `backend/internal/yaml/handler.go` lines 234-236
- All error responses from the k8s API GET are mapped to 404 regardless of the actual status code.
- The existing `mapK8sError` helper in `resources/errors.go` already handles this mapping correctly.

## Recommendation

Use the existing `mapK8sError` pattern from `backend/internal/k8s/resources/errors.go` to translate k8s API errors into appropriate HTTP status codes (403, 404, 409, etc.).
