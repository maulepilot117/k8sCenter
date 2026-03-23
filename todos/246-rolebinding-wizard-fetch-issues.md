---
status: complete
priority: p2
issue_id: 246
tags: [frontend, reliability, code-review, phase4b]
---

# RoleBindingWizard fetch issues (AbortController + localUsers guard)

## Problem Statement

The RoleBindingWizard has two fetch-related issues that can cause stale data and unnecessary re-fetching.

## Findings

### (a) Missing AbortController for namespace-dependent Roles fetch
When the namespace changes, the Roles fetch fires a new request but does not cancel the previous in-flight request. If the previous request completes after the new one, stale data from the old namespace will overwrite the current results.

### (b) Incorrect empty-result guard for localUsers fetch
The guard `localUsers.value.length === 0` causes the fetch to retry on every render when the result is legitimately empty (no local users exist). This should use a boolean `fetched` signal to track whether the fetch has been attempted, rather than checking the result length.

## Technical Details

- **File:** `frontend/islands/RoleBindingWizard.tsx`
- (a) The `useEffect` that fetches Roles based on the selected namespace does not create or use an `AbortController`
- (b) The `useEffect` for fetching local users uses `localUsers.value.length === 0` as the fetch guard

## Acceptance Criteria

- [ ] Roles fetch uses an `AbortController` that aborts the previous request when namespace changes, with cleanup in the effect's return function
- [ ] Local users fetch uses a `fetched` boolean signal instead of checking array length
- [ ] No unnecessary re-fetching occurs when the local users list is legitimately empty
- [ ] Stale namespace role data cannot overwrite current results
