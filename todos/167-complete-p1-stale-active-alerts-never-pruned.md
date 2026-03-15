---
status: complete
priority: p1
issue_id: "167"
tags: [code-review, data-integrity, step-11]
dependencies: []
---

# Stale Active Alerts Persist Forever — No TTL on Active Map

## Problem Statement
`Prune()` only removes entries from `history`, never from `active`. If Alertmanager fails to send a resolved notification (restart, rule deletion, network partition), the alert stays in `s.active` forever. `ActiveAlerts()` will return stale alerts indefinitely, misleading operators.

## Findings
- **Source**: Data Integrity review (P1-1)
- **Location**: `backend/internal/alerting/store.go:218-233` (Prune only touches history)

## Proposed Solutions

### Option A: Add staleness TTL to Prune (Recommended)
In `Prune()`, also iterate `s.active` and remove entries where `StartsAt` (or last `ReceivedAt`) is older than a configurable staleness threshold (e.g., 24 hours without re-fire).
- **Pros**: Simple, prevents unbounded active alert growth
- **Cons**: Could remove a legitimately long-running alert
- **Effort**: Small
- **Risk**: Low (24h is generous)

## Acceptance Criteria
- [ ] Active alerts older than staleness threshold are pruned
- [ ] Pruned active alerts get a "resolved (stale)" entry in history
- [ ] Test verifying stale alerts are cleaned up

## Resources
- PR: #17
- File: `backend/internal/alerting/store.go:218-233`
