---
status: pending
priority: p2
issue_id: "271"
tags: [code-review, correctness, backend]
dependencies: []
---

# UpdateProvider Sets Empty Strings Instead of Removing Keys

## Problem Statement

When `input.Channel` or `input.Address` are empty strings, the Update functions write empty strings into the CRD spec rather than removing the keys. For Flux CRDs, empty string values are semantically different from absent fields. The `SecretRef` field correctly uses a conditional check, but in reverse — an empty string is ignored, meaning users cannot clear a previously-set secretRef.

## Findings

**Identified by:** Architecture Strategist

**Evidence:**
- `flux_notifications.go:472-473` — empty channel/address written as empty strings
- `flux_notifications.go:475` — SecretRef conditionally set (non-empty only), but cannot be cleared

## Proposed Solutions

### Option A: Conditional set + explicit clear (Recommended)
- If field is non-empty: set the value
- If field is empty string: delete the key from existingSpec
- Apply consistently to all optional fields (channel, address, secretRef, proxy, certSecretRef)
- **Effort:** Small
- **Risk:** Low

## Acceptance Criteria

- [ ] Empty string on optional fields removes the key from spec
- [ ] Non-empty values are set as before
- [ ] SecretRef can be explicitly cleared
- [ ] Tests cover empty-string-clears-field behavior

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | Semantic difference between empty string and absent field in K8s CRDs |

## Resources

- PR: #153
- File: backend/internal/notification/flux_notifications.go
