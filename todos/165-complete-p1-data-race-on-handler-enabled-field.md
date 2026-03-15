---
status: complete
priority: p1
issue_id: "165"
tags: [code-review, concurrency, step-11]
dependencies: []
---

# Data Race on Handler.Enabled Field + ConfigMu Not Deferred

## Problem Statement
`HandleWebhook` reads `h.Enabled` (line 41) without holding `ConfigMu.RLock()`, while `HandleUpdateSettings` writes it (line 433) under `ConfigMu.Lock()`. This is a data race detectable by `go test -race`. Additionally, `ConfigMu.Unlock()` on line 434 is explicit (not deferred), risking permanent deadlock if future code panics. The notifier config update on line 438 reads `h.Config.SMTP` after releasing the lock.

## Findings
- **Source**: Architecture review (P1), Data Integrity review (P1), Security review (L3)
- **Locations**: `backend/internal/alerting/handler.go:41,412-438`

## Proposed Solutions

### Option A: Use atomic.Bool for Enabled + defer Unlock + capture config before unlock (Recommended)
1. Change `Enabled bool` to `enabled atomic.Bool` on Handler
2. Use `defer h.ConfigMu.Unlock()`
3. Capture SMTP config into a local var while lock is held, pass to notifier after unlock
- **Pros**: Eliminates all three issues cleanly
- **Cons**: Minor refactor
- **Effort**: Small
- **Risk**: None

## Acceptance Criteria
- [ ] `go test -race` passes with concurrent webhook + settings update
- [ ] ConfigMu uses defer
- [ ] Notifier receives consistent config snapshot

## Resources
- PR: #17
- File: `backend/internal/alerting/handler.go:41,412-438`
