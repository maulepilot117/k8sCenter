---
status: pending
priority: p2
issue_id: "199"
tags: [code-review, consistency, validation]
dependencies: []
---

# List Handler Skips parseSelectorOrReject Validation

## Problem Statement

All existing list handlers call `parseSelectorOrReject(w, params.LabelSelector)` to validate the label selector before querying. The Cilium list handler passes `params.LabelSelector` directly to the Kubernetes API as a raw string, relying on server-side validation which returns less-friendly error messages.

## Findings

- **cilium.go:~76**: No `parseSelectorOrReject` call
- **networkpolicies.go:21**: Shows the pattern — `sel, ok := parseSelectorOrReject(w, params.LabelSelector)`
- Note: `parseSelectorOrReject` returns a `labels.Selector` for informer-based list, but the dynamic client takes a string. Still worth validating format before passing.
- Also: access check has redundant if/else (lines 78-87) — `ns` is already `""` in the else branch
- Found by: Pattern Recognition

## Proposed Solutions

### Option A: Validate selector format, collapse access check
Validate the label selector string and collapse the redundant if/else access check.
- Effort: Small
- Risk: Low

## Acceptance Criteria
- [ ] Label selector validated before passing to API
- [ ] Redundant if/else access check collapsed to single call

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- File: `backend/internal/k8s/resources/cilium.go:76-87`
