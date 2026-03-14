---
status: pending
priority: p3
issue_id: "161"
tags: [code-review, cleanup, step-10]
dependencies: []
---

# Replace unstructuredHelper with unstructured.NestedString/Bool from apimachinery

## Problem Statement
The custom `unstructuredHelper` type (storage/handler.go:333-372, 40 lines + 47 lines of tests) provides generic nested map traversal used exactly 7 times. `k8s.io/apimachinery` already provides `unstructured.NestedString` and `unstructured.NestedBool` which do exactly this.

## Findings
- **Agents**: code-simplicity-reviewer (Finding 6)
- **Location**: `backend/internal/storage/handler.go:333-372`, `storage_test.go:198-244`

## Proposed Solutions
Replace with `unstructured.NestedString(obj, "metadata", "name")` and `unstructured.NestedBool(obj, "status", "readyToUse")`. Saves ~87 LOC.

- **Effort**: Small
- **Risk**: Low

## Work Log
- 2026-03-14: Identified by code-simplicity-reviewer

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
