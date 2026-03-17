---
status: pending
priority: p3
issue_id: "203"
tags: [code-review, testing]
dependencies: []
---

# Add Unit Tests for Cilium Validation Functions

## Problem Statement

No `cilium_test.go` exists. The validation logic (`validateCiliumPolicy`, `validateRule`, `validateCIDR`, `buildCiliumPolicy`, `buildDirectionalRules`) is substantial and testable in isolation. These are the security boundary — they should have tests.

## Findings

- **cilium.go**: ~150 lines of validation/build logic with no test coverage
- `resources_test.go` covers 19 tests for other handlers
- Pure functions with no external deps — ideal test targets
- Found by: Architecture Strategist

## Acceptance Criteria
- [ ] Tests for validateCiliumPolicy (valid + invalid cases)
- [ ] Tests for validateRule (each peerType, invalid action)
- [ ] Tests for validateCIDR (valid, invalid, loopback)
- [ ] Tests for buildCiliumPolicy (ingress/egress/deny structure)

## Work Log
- 2026-03-16: Created from PR #36 code review
