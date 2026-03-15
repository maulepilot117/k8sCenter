---
status: complete
priority: p1
issue_id: "168"
tags: [code-review, security, step-11]
dependencies: []
---

# SMTP Header Injection via CRLF in Alert Labels

## Problem Statement
The email subject is constructed from alert labels (`alertname`, `severity`) without sanitizing CRLF characters. An attacker who can create PrometheusRules could set `alertname` to a value containing `\r\n`, injecting arbitrary SMTP headers (Bcc, additional recipients, modified content-type).

## Findings
- **Source**: Security review (M1)
- **Location**: `backend/internal/alerting/notifier.go:215-225` (subject construction), `notifier.go:306-307` (subject written to SMTP body)

## Proposed Solutions

### Option A: Strip CRLF from subject (Recommended)
```go
subject = strings.ReplaceAll(subject, "\r", "")
subject = strings.ReplaceAll(subject, "\n", "")
```
- **Pros**: Simple, complete fix
- **Cons**: None
- **Effort**: Small (2 lines)
- **Risk**: None

## Acceptance Criteria
- [ ] Email subjects with CRLF characters are sanitized
- [ ] Test with alertname containing \r\n verifies no header injection

## Resources
- PR: #17
- File: `backend/internal/alerting/notifier.go:215-225,306-307`
