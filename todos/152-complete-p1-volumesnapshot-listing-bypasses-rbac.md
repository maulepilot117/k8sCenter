---
status: pending
priority: p1
issue_id: "152"
tags: [code-review, security, step-10]
dependencies: []
---

# VolumeSnapshot Listing Bypasses User RBAC Impersonation

## Problem Statement
`HandleListSnapshots` and `getSnapshotDrivers` use `h.K8sClient.BaseDynamicClient()` which operates with the service account's own permissions. Any authenticated user can list VolumeSnapshots across all namespaces and VolumeSnapshotClasses regardless of their Kubernetes RBAC permissions.

## Findings
- **Agents**: security-sentinel (HIGH-01), architecture-strategist (P1), data-integrity-guardian (P2-FINDING-6)
- **Location**: `backend/internal/storage/handler.go:122` (HandleListSnapshots), `handler.go:189` (getSnapshotDrivers)
- **Evidence**: Both use `BaseDynamicClient()` instead of `DynamicClientForUser()`.

## Proposed Solutions

### Option A: Use DynamicClientForUser for snapshot listing
- Pass user credentials from handler to listing functions
- Use `k8sClient.DynamicClientForUser(username, groups)` for VolumeSnapshot queries
- Keep `BaseDynamicClient` for `getSnapshotDrivers` (VolumeSnapshotClasses are cluster metadata, acceptable to read with service account)
- **Pros**: Enforces RBAC, consistent with architecture principles
- **Cons**: Requires threading user identity through handler
- **Effort**: Small
- **Risk**: Low

## Recommended Action
Option A — straightforward refactor, aligns with architecture principles.

## Technical Details
- **Affected files**: `backend/internal/storage/handler.go`

## Acceptance Criteria
- [ ] `HandleListSnapshots` uses impersonated dynamic client
- [ ] User identity extracted from request context and passed through
- [ ] Tests updated

## Work Log
- 2026-03-14: Identified by 3 review agents

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
