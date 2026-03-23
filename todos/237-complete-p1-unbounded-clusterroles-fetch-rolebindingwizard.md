---
status: pending
priority: p1
issue_id: 237
tags: [performance, code-review, phase4b]
---

# Unbounded ClusterRoles fetch in RoleBindingWizard

## Problem Statement

The RoleBindingWizard fetches ALL ClusterRoles with no `?limit=` parameter. Managed clusters (EKS/GKE/AKS) can have 200+ ClusterRoles, each 2-10KB with nested PolicyRule arrays. This could produce 200KB-2MB responses, causing slow load times and excessive memory usage in the browser.

## Findings

- The `apiGet` call for ClusterRoles has no limit parameter, fetching the entire list.
- Managed Kubernetes environments commonly have 200+ ClusterRoles with verbose PolicyRule arrays.
- Response sizes could reach 200KB-2MB, impacting page load performance.
- No pagination or lazy-loading mechanism is used for the dropdown/selector.

## Technical Details

- **Affected file:** `frontend/islands/RoleBindingWizard.tsx`, line 97

## Acceptance Criteria

- [ ] Add `?limit=500` to the ClusterRoles API call to cap the response size
- [ ] Verify the wizard handles the case where more than 500 ClusterRoles exist (e.g. show a warning or support search/filter)
- [ ] Long-term: consider adding a names-only query mode to the backend to return only metadata without full PolicyRule arrays
