---
status: complete
priority: p2
issue_id: 242
tags: [consistency, frontend, code-review, phase4b]
---

# Wizard routes use /resources/ prefix instead of /rbac/ convention

## Problem Statement

All existing wizards use section-based paths (e.g., `/workloads/deployments/new`, `/networking/services/new`). The RoleBinding and ClusterRoleBinding wizard routes are placed under `/resources/` while their list pages are at `/rbac/`. This creates a URL mismatch and breaks the established routing convention.

## Findings

The RBAC wizard "new" pages are located at `/resources/rolebindings/new` and `/resources/clusterrolebindings/new`, while the corresponding list pages are at `/rbac/rolebindings` and `/rbac/clusterrolebindings`. Every other resource type keeps its wizard under the same section prefix as its list page.

## Technical Details

- **Files:**
  - `frontend/routes/resources/rolebindings/new.tsx`
  - `frontend/routes/resources/clusterrolebindings/new.tsx`
- Should be moved to:
  - `frontend/routes/rbac/rolebindings/new.tsx`
  - `frontend/routes/rbac/clusterrolebindings/new.tsx`
- `createHref` references will need updating to match

## Acceptance Criteria

- [ ] Wizard routes moved to `routes/rbac/rolebindings/new.tsx` and `routes/rbac/clusterrolebindings/new.tsx`
- [ ] `createHref` and any navigation links updated to use `/rbac/` prefix
- [ ] Cancel buttons and breadcrumbs navigate back to the correct `/rbac/` list pages
- [ ] Old `/resources/` paths no longer serve the wizard pages
