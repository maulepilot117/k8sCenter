---
status: pending
priority: p2
issue_id: "175"
tags: [code-review, architecture, step-11]
dependencies: []
---

# No To/Recipients Field for SMTP — Emails Sent Only to From Address

## Problem Statement
The SMTP code uses `from` as both sender and recipient. There is no `Recipients` or `To` field in `SMTPConfig`. Alert emails can only be sent to the sender address, making the feature impractical. The email also lacks a `To:` header (RFC 2822 violation).

## Findings
- **Source**: Architecture review (P2), Security review (M2)
- **Location**: `backend/internal/alerting/notifier.go:373-374,419`, `backend/internal/config/config.go`

## Proposed Solutions
### Option A: Add Recipients []string to AlertingConfig
Add a `Recipients` field, use it for SMTP RCPT and To header. Fall back to From if empty.
- **Effort**: Small
- **Risk**: None

## Resources
- PR: #17
