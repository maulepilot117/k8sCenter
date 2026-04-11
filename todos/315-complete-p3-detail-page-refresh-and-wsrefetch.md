---
status: pending
priority: p3
issue_id: "315"
tags: [code-review, ux, real-time, pr-167]
dependencies: []
---

# VulnerabilityDetail has no Refresh button and no useWsRefetch

## Problem Statement

`VulnerabilityDetail` only loads CVE data on mount. There's no Refresh button and no `useWsRefetch` integration. The dashboard (list view) has a Refresh button but detail pages don't.

**Why it matters:** After remediating a CVE, users want to verify the next scan shows the fix. Currently they have to reload the page.

## Findings

### Pattern Recognition Reviewer

**File:** `frontend/islands/VulnerabilityDetail.tsx`

**Compare:**
- `frontend/islands/GitOpsAppDetail.tsx:107-109` — uses `useWsRefetch`
- `frontend/islands/GitOpsAppDetail.tsx:304-311` — Refresh button
- `frontend/islands/ViolationBrowser.tsx:56-59` — uses `useWsRefetch`

## Proposed Solutions

### Option A: Add Refresh button

Mirror the dashboard's refresh button pattern in the detail header.

**Pros:** Simple; matches dashboard
**Cons:** No live updates
**Effort:** Trivial
**Risk:** None

### Option B: Add useWsRefetch with VulnerabilityReport topic

Check if the backend has a websocket watch for VulnerabilityReport CRDs. If yes, subscribe.

**Pros:** Live updates
**Cons:** Backend may not support it yet
**Effort:** Small (frontend) or Medium (with backend support)
**Risk:** Low

## Recommended Action

<!-- Filled during triage — Option A as first step -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx`

## Acceptance Criteria

- [ ] Refresh button on detail page header
- [ ] Optional: live updates via websocket

## Work Log

## Resources

- PR #167
