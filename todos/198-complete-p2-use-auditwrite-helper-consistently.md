---
status: pending
priority: p2
issue_id: "198"
tags: [code-review, architecture, consistency]
dependencies: []
---

# Use auditWrite Helper Instead of Direct AuditLogger.Log

## Problem Statement

`HandleCreateCiliumPolicy` manually constructs an `audit.Entry` and calls `h.AuditLogger.Log()` directly to include a `Detail` field with `policySummary()`. All other handlers use the `h.auditWrite()` helper. This creates two code paths for audit logging.

## Findings

- **cilium.go:215-228**: Direct `h.AuditLogger.Log()` call
- **cilium.go:506-518**: `policySummary()` helper only used here
- **handler.go:89-101**: Standard `auditWrite` helper used everywhere else
- Found by: Architecture, Pattern Recognition, Simplicity reviewers

## Proposed Solutions

### Option A: Use auditWrite, drop policySummary
Replace with `h.auditWrite(r, user, audit.ActionCreate, "CiliumNetworkPolicy", ns, created.GetName(), audit.ResultSuccess)`. Remove `policySummary()`.
- Effort: Small
- Risk: Low (loses detail field, but no other handler uses it)

## Acceptance Criteria
- [ ] Create handler uses `auditWrite` helper
- [ ] `policySummary` function removed
- [ ] Audit logging consistent with all other handlers

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- Files: `cilium.go:215-228,506-518`, `handler.go:89-101`
