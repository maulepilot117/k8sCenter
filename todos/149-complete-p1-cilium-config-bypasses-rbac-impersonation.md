---
status: pending
priority: p1
issue_id: "149"
tags: [code-review, security, architecture, step-10]
dependencies: []
---

# Cilium Config Operations Bypass Kubernetes RBAC Impersonation

## Problem Statement
`ReadCiliumConfig` and `UpdateCiliumConfig` use `k8sClient.BaseClientset()` which operates with the KubeCenter service account's own permissions, not the authenticated user's impersonated identity. This violates the project's foundational architecture principle: "All Kubernetes API calls go through user impersonation. Never use the service account's own permissions for user-initiated actions." Any authenticated KubeCenter user can read and modify the Cilium ConfigMap regardless of their Kubernetes RBAC permissions, enabling privilege escalation.

Additionally, the Helm ClusterRole only grants `get, list, watch` on ConfigMaps — it lacks `update`, meaning `UpdateCiliumConfig` will actually fail with 403 in production. This is a secondary symptom: the correct fix is impersonation, not broadening service account permissions.

## Findings
- **Agents**: security-sentinel (CRITICAL-01), architecture-strategist (P1), data-integrity-guardian (P1-FINDING-2), pattern-recognition-specialist (P1)
- **Location**: `backend/internal/networking/cilium.go:29` (ReadCiliumConfig), `backend/internal/networking/cilium.go:50-67` (UpdateCiliumConfig)
- **Evidence**: Both functions call `k8sClient.BaseClientset()`. The handler at `handler.go:102` extracts the authenticated user but never passes credentials for impersonation. Every other write operation in the codebase uses `ClientForUser`.

## Proposed Solutions

### Option A: Refactor to accept user credentials and use ClientForUser
- Pass `user.KubernetesUsername` and `user.KubernetesGroups` from the handler to both `ReadCiliumConfig` and `UpdateCiliumConfig`
- Use `k8sClient.ClientForUser(username, groups)` for the ConfigMap operations
- **Pros**: Consistent with all other write operations, enforces Kubernetes RBAC, no Helm changes needed
- **Cons**: Users need ConfigMap read/update RBAC in kube-system/cilium namespace
- **Effort**: Small
- **Risk**: Low

## Recommended Action
Option A — this is a straightforward refactor that aligns with the project's core security principle.

## Technical Details
- **Affected files**: `backend/internal/networking/cilium.go`, `backend/internal/networking/handler.go`
- **Components**: CNI config read/write, Kubernetes RBAC enforcement

## Acceptance Criteria
- [ ] `ReadCiliumConfig` uses impersonated client
- [ ] `UpdateCiliumConfig` uses impersonated client
- [ ] Handler passes user identity to both functions
- [ ] Helm ClusterRole does NOT need `update` on ConfigMaps (user RBAC governs)
- [ ] `go test -race ./internal/networking/...` passes

## Work Log
- 2026-03-14: Identified by 4 review agents (security, architecture, data-integrity, pattern-recognition)

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
- Architecture principle: CLAUDE.md "All Kubernetes API calls go through user impersonation"
