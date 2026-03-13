---
status: pending
priority: p3
issue_id: "091"
tags: [code-review, backend, security, websocket]
dependencies: []
---

# RBAC Denial Error Leaks Namespace Information

## Problem Statement
When a WebSocket subscription RBAC check fails, the error message includes the namespace name. This leaks information about namespace existence to users who may not have permission to list namespaces.

## Findings
- Error message format: "access denied to {kind} in namespace {namespace}"
- User may not have list-namespaces permission
- Should return a generic "subscription denied" without revealing namespace

## Proposed Solutions
### Option A: Return generic "subscription denied" without namespace details
- **Effort:** Small
- **Risk:** Low
