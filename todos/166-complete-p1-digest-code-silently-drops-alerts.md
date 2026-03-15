---
status: complete
priority: p1
issue_id: "166"
tags: [code-review, correctness, step-11]
dependencies: []
---

# Digest Code Silently Drops Alert Email Notifications

## Problem Statement
When `shouldDigest()` returns true (>5 alerts in 1 minute), `QueueAlert` returns `true` (claiming success) but no email is ever sent. The `digestQueue` field is declared but never written to or consumed. The `alert_digest.html` template exists but is never rendered. During alert storms, all individual email notifications are silently dropped with no digest replacement.

## Findings
- **Source**: Simplicity review (Finding 1), Performance review (Finding 3), Data Integrity review
- **Locations**: `backend/internal/alerting/notifier.go:77-79,137-139,227-243`, `templates/alert_digest.html`

## Proposed Solutions

### Option A: Remove digest system entirely (Recommended)
Remove `digestMu`, `digestWindow`, `digestQueue` fields, `shouldDigest()` method, and the `shouldDigest()` call in `QueueAlert`. Remove `alert_digest.html` template. Rate limiting and per-alert cooldown already prevent email flooding.
- **Pros**: Eliminates bug, removes ~60 LOC of dead code, simplifies notifier
- **Cons**: No digest feature (can be built properly later if needed)
- **Effort**: Small
- **Risk**: None

### Option B: Implement digest properly
Add a timer goroutine that, after 1-minute window, renders digest template and sends it.
- **Pros**: Feature works as designed
- **Cons**: Significant complexity for MVP
- **Effort**: Medium
- **Risk**: Medium (new concurrency)

## Acceptance Criteria
- [ ] No alerts silently dropped during high-volume periods
- [ ] No dead code remaining (digestQueue, shouldDigest, digest template if removing)
- [ ] Rate limiting still prevents email flooding

## Resources
- PR: #17
- File: `backend/internal/alerting/notifier.go:77-79,137-139,227-243`
