---
status: pending
priority: p2
issue_id: "156"
tags: [code-review, security, step-10]
dependencies: []
---

# Error Messages Leak Internal Kubernetes Error Details

## Problem Statement
Multiple handlers pass `err.Error()` as the `detail` field in error responses, exposing internal Kubernetes API server error messages (service URLs, namespace paths, RBAC details) to clients. The project's architecture rules state: "Error handling: never expose internal errors to users."

## Findings
- **Agents**: security-sentinel (MEDIUM-01)
- **Location**: `backend/internal/networking/handler.go:98,137`, `backend/internal/storage/handler.go:50,86,136`

## Proposed Solutions
Log full errors server-side with `slog` and return empty string or generic message as detail field, consistent with existing handlers.

- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] All error responses in storage/networking handlers use empty or generic detail strings
- [ ] Full errors logged server-side

## Work Log
- 2026-03-14: Identified by security-sentinel

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
