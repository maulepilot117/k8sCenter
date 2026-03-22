---
status: pending
priority: p2
issue_id: "223"
tags: [code-review, validation, backend, exec, overflow]
dependencies: []
---

# Exec Resize uint16 Overflow

## Problem Statement

Resize message `cols` and `rows` values are cast to `uint16` without upper bound checking. A value of 65537 wraps to 1 due to integer overflow, resulting in a 1-column or 1-row terminal — effectively unusable. Malicious or buggy clients could exploit this.

**Location:** `backend/internal/k8s/resources/pods.go` lines 400-403

## Proposed Solutions

### Option A: Add upper bound validation before cast
- Validate that `cols <= 500 && rows <= 500` (reasonable terminal size limits) before casting to `uint16`. Return an error message over the WebSocket if values are out of range.
- **Effort:** Low — a few lines of validation.
- **Risk:** Low — simple bounds check.

### Option B: Clamp values to valid range
- Clamp cols to [1, 500] and rows to [1, 200] silently instead of rejecting.
- **Effort:** Low.
- **Risk:** Low — but silent clamping may hide client bugs.

## Acceptance Criteria

- [ ] Values exceeding reasonable terminal dimensions (e.g., >500 cols, >500 rows) are rejected or clamped
- [ ] Values of 0 are rejected (minimum 1x1)
- [ ] uint16 overflow is no longer possible
- [ ] Valid resize messages continue to work normally

## Work Log

- 2026-03-22: Created from Phase 4A code review.
