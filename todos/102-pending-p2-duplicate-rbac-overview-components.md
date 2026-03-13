---
status: pending
priority: p2
issue_id: "102"
tags: [code-review, quality, step-6]
dependencies: []
---

# Duplicate RBAC Overview Components (Role/ClusterRole, RoleBinding/ClusterRoleBinding)

## Problem Statement
`RoleOverview.tsx` and `ClusterRoleOverview.tsx` are near-identical (67 lines each) — same "Rules" table, only the type assertion differs. Similarly, `RoleBindingOverview.tsx` and `ClusterRoleBindingOverview.tsx` are near-identical (91/93 lines) — same "Role Reference" section and "Subjects" table. This is ~160 lines of duplicated code.

## Findings
- **Agent**: pattern-recognition-specialist, code-simplicity-reviewer (PR #6 review)
- **Location**: `frontend/components/k8s/detail/RoleOverview.tsx`, `ClusterRoleOverview.tsx`, `RoleBindingOverview.tsx`, `ClusterRoleBindingOverview.tsx`
- The types `Role`/`ClusterRole` share the exact same `rules` shape; `RoleBinding`/`ClusterRoleBinding` share the exact same `subjects`/`roleRef` shape

## Proposed Solutions

### Option A: Extract Shared Components (Recommended)
Create `RulesTable.tsx` (for Role/ClusterRole) and `BindingOverview.tsx` (for RoleBinding/ClusterRoleBinding). Both Role and ClusterRole use the shared `RulesTable`; both Binding types use the shared `BindingOverview`.
- **Pros**: Eliminates 160 lines of duplication, single place to fix bugs
- **Cons**: Slight indirection
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] No duplicate rendering logic between Role/ClusterRole overviews
- [ ] No duplicate rendering logic between RoleBinding/ClusterRoleBinding overviews
- [ ] Both pairs still render identically to current behavior
