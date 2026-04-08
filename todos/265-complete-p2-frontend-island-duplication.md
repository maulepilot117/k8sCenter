---
status: pending
priority: p2
issue_id: "265"
tags: [code-review, quality, frontend, duplication]
dependencies: []
---

# Frontend Island Massive Duplication (~1,400 extractable lines)

## Problem Statement

FluxProviders.tsx (738 LOC), FluxAlerts.tsx (855 LOC), FluxReceivers.tsx (876 LOC) share ~500+ lines of identical structural code. This is the single largest code quality issue in the PR. The three islands differ only in: API endpoint path, form fields, table columns, and type constants.

Duplicated elements include:
- 16 identical signal declarations per island
- Fetch function (Promise.all pattern)
- useEffect hooks (initial fetch + outside-click handler)
- handleRefresh, handleSuspendToggle, handleDelete functions
- Page layout JSX (header bar, unavailable banner, search, pagination, empty state)
- Actions dropdown (Edit/Suspend/Delete)
- Modal form shell (backdrop, dialog, Escape handler, inputClass, Cancel/Submit)
- SourceCountBadge (FluxAlerts:580) and ResourceCountBadge (FluxReceivers:631) are byte-for-byte identical

## Findings

**Identified by:** Code Simplicity, Pattern Recognition, Architecture Strategist (all 3 agents flagged this independently)

**Evidence:**
- `frontend/islands/FluxProviders.tsx` — 738 lines
- `frontend/islands/FluxAlerts.tsx` — 855 lines
- `frontend/islands/FluxReceivers.tsx` — 876 lines
- `inputClass` constant defined identically in all 3 form modals
- `PAGE_SIZE = 100` defined independently in all 3 islands
- `EVENT_SOURCE_KINDS` (FluxAlerts:26-37) duplicated as `RESOURCE_KINDS` (FluxReceivers:39-50)

## Proposed Solutions

### Option A: Extract shared hook + layout component (Recommended)
- Create `useNotificationCrud()` hook: signals, fetch, refresh, suspend, delete (~100 lines x 3 saved)
- Create `NotificationPageLayout`: header, banner, search, pagination, empty state (~80 lines x 3 saved)
- Create `ActionsDropdown` component (~40 lines x 3 saved)
- Merge SourceCountBadge/ResourceCountBadge into single `CountBadge` in NotificationBadges.tsx
- Extract `inputClass`, `PAGE_SIZE`, `EVENT_SOURCE_KINDS` to shared modules
- **Pros:** ~1,200 lines saved, each island becomes ~300 lines of unique form/table config
- **Cons:** Medium effort, adds indirection
- **Effort:** Medium
- **Risk:** Low

### Option B: Parameterized generic island component
- Single `NotificationCRUDIsland<T>` parameterized by resource config
- **Pros:** Maximum deduplication (~1,400 lines saved)
- **Cons:** Higher complexity, harder to customize per-resource
- **Effort:** Large
- **Risk:** Medium

## Recommended Action

Option A — extract hook + layout + shared components. Keeps each island readable while eliminating mechanical duplication.

## Technical Details

**Affected files:**
- `frontend/islands/FluxProviders.tsx`
- `frontend/islands/FluxAlerts.tsx`
- `frontend/islands/FluxReceivers.tsx`
- `frontend/components/ui/NotificationBadges.tsx` (add CountBadge)
- New: `frontend/lib/useNotificationCrud.ts` (shared hook)
- New: `frontend/components/NotificationPageLayout.tsx` (shared layout)

## Acceptance Criteria

- [ ] All 3 islands use shared hook for signals + CRUD actions
- [ ] Shared layout component for header/banner/search/pagination/empty state
- [ ] SourceCountBadge and ResourceCountBadge merged into single component
- [ ] PAGE_SIZE, inputClass, EVENT_SOURCE_KINDS extracted to shared modules
- [ ] No functional regression — all E2E tests pass

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | 3/6 agents flagged this independently — highest-signal finding |

## Resources

- PR: #153
- Files: frontend/islands/Flux{Providers,Alerts,Receivers}.tsx
