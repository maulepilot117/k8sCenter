---
status: complete
priority: p3
issue_id: "014"
tags: [code-review, quality]
dependencies: []
---

# storedUser-to-User Conversion Duplicated 3 Times + Cleanup Signal Inconsistency

## Problem Statement
1. `storedUser` to `User` conversion appears in 3 places in `local.go` (lines 76, 120, 143). Should be a `toUser()` method.
2. Cleanup goroutine signals are inconsistent: `ClientFactory` uses `context.Context`, `SessionStore` and `RateLimiter` use `<-chan struct{}`. Should standardize on `context.Context`.

## Proposed Solutions
- Add `func (s storedUser) toUser() *User` method
- Change `SessionStore.StartCleanup` and `RateLimiter.StartCleanup` to accept `context.Context`
- **Effort**: Small

## Acceptance Criteria
- [ ] `toUser()` method eliminates conversion duplication
- [ ] All cleanup goroutines use `context.Context` consistently
