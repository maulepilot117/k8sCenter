---
status: pending
priority: p3
issue_id: 251
tags: [consistency, pre-existing, code-review, phase4b]
---

# Pre-existing inconsistent checkAccess ordering in cluster-scoped RBAC list handlers

## Problem Statement

The cluster-scoped RBAC list handlers call `checkAccess` before `parseSelectorOrReject`, which is the opposite order from all namespaced handlers and other resource handlers. This is a pre-existing inconsistency inherited from the original rbac_viewer.go implementation.

## Findings

- `HandleListClusterRoles` and `HandleListClusterRoleBindings` call `checkAccess` BEFORE `parseSelectorOrReject`
- All namespaced handlers (e.g., Roles, RoleBindings) and other resource handlers (configmaps, services, etc.) call `parseSelectorOrReject` first, then `checkAccess`
- The practical impact is minimal — both checks must pass for the request to proceed — but the inconsistency makes the codebase harder to reason about
- This pattern existed in the original rbac_viewer.go before Phase 4B changes

## Technical Details

Affected files:
- `backend/internal/k8s/resources/rbac.go` — HandleListClusterRoles, HandleListClusterRoleBindings

## Acceptance Criteria

- [ ] Reorder HandleListClusterRoles to call parseSelectorOrReject before checkAccess
- [ ] Reorder HandleListClusterRoleBindings to call parseSelectorOrReject before checkAccess
- [ ] Verify consistent ordering matches namespaced handlers and other resource types
