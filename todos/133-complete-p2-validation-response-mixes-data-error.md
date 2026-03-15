---
status: pending
priority: p2
issue_id: "133"
tags: [code-review, backend, api-design]
dependencies: []
---

# Validation Response Mixes Data and Error in Same Envelope

## Problem Statement
`writeValidationErrors` in `handler.go` sets both `Error` and `Data` on the same `api.Response`. The project convention uses `Data` for success and `Error` for errors, never both simultaneously. This hybrid breaks the established API contract.

## Findings
- `handler.go` lines 543-551
- YAML validate handler uses a different but self-consistent pattern (errors in Data with 200)

## Proposed Solutions

### Option A: Put field errors inside APIError.Detail as JSON string
- **Effort:** Small
- **Risk:** Low — frontend needs to parse Detail

### Option B: Add Fields property to APIError struct
- **Pros:** Type-safe, clear contract
- **Cons:** Changes shared type
- **Effort:** Small
- **Risk:** Low

## Acceptance Criteria
- [ ] Validation errors follow single-side envelope pattern
- [ ] Frontend updated to parse new format
- [ ] Tests updated

## Work Log
- 2026-03-13: Created from PR #14 code review

## Resources
- PR: #14 | Agent: architecture-strategist
