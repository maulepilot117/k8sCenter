---
status: complete
priority: p1
issue_id: "163"
tags: [code-review, security, step-11]
dependencies: []
---

# Empty Webhook Token Authentication Bypass

## Problem Statement
When `KUBECENTER_ALERTING_WEBHOOKTOKEN` is not configured (empty string), `subtle.ConstantTimeCompare([]byte(""), []byte(""))` returns 1 (equal), making the webhook endpoint completely unauthenticated. An attacker can inject arbitrary alerts, trigger email notifications, and populate the alert store with fabricated data.

## Findings
- **Source**: Security review (C1)
- **Location**: `backend/internal/alerting/handler.go:51-57`
- **Evidence**: `token := strings.TrimPrefix(authHeader, "Bearer ")` produces empty string when header is `"Bearer "`, and `h.WebhookToken` defaults to empty string

## Proposed Solutions

### Option A: Guard at handler level (Recommended)
Add check at top of `HandleWebhook`: if `h.WebhookToken == ""`, return 503 "webhook token not configured".
- **Pros**: Simple, clear error message, defense in depth
- **Cons**: None
- **Effort**: Small
- **Risk**: None

### Option B: Guard at startup
Refuse to enable alerting when `WebhookToken` is empty (in main.go).
- **Pros**: Fail-fast, prevents misconfiguration
- **Cons**: More restrictive — some users may want alerting without webhook
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Webhook returns 401 or 503 when WebhookToken is empty/unconfigured
- [ ] Test added for empty token scenario
- [ ] Existing tests still pass

## Resources
- PR: #17
- File: `backend/internal/alerting/handler.go:51-57`
