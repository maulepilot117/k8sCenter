---
status: complete
priority: p2
issue_id: 241
tags: [validation, code-review, phase4b]
---

# ClusterRoleBinding wizard allows referencing a Role (invalid in k8s)

## Problem Statement

The RoleBinding wizard's `Validate()` function allows `roleRef.Kind == "Role"` when `ClusterScope == true`. Kubernetes will reject a ClusterRoleBinding that references a Role (only ClusterRole is valid for ClusterRoleBindings). This should be caught at wizard validation time rather than failing at the API server.

## Findings

A ClusterRoleBinding can only reference a ClusterRole, never a namespaced Role. The wizard validation does not enforce this constraint, allowing users to submit an invalid configuration that will be rejected by the Kubernetes API with a confusing error.

## Technical Details

- **File:** `backend/internal/wizard/rolebinding.go`, lines 48-51
- The `Validate()` method checks `roleRef.Kind` is either "Role" or "ClusterRole" but does not cross-validate against `ClusterScope`

## Acceptance Criteria

- [ ] Wizard validation returns an error when `ClusterScope == true` and `RoleRef.Kind == "Role"`
- [ ] Error message clearly states that ClusterRoleBindings can only reference ClusterRoles
- [ ] Unit test covers the invalid combination
- [ ] Valid combinations (ClusterScope+ClusterRole, namespaced+Role, namespaced+ClusterRole) continue to pass validation
