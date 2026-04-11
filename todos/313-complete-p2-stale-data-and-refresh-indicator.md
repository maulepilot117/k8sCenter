---
status: pending
priority: p2
issue_id: "313"
tags: [code-review, ux, pr-167]
dependencies: []
---

# No stale-data indicator or loading overlay on refresh

## Problem Statement

Two related UX issues:

1. **`lastScanned` is shown but without relative time or staleness warning.** Trivy results lag image changes by minutes to hours. A scan that's 3 days old looks identical to one that's 30 seconds old. The detail page empty-state acknowledges lag, but nothing warns when *displayed* data is stale.

2. **Refresh button has no visible effect on the data area.** The button label changes to "Refreshing..." but the table keeps showing stale rows. On slow networks users can't tell if the refresh did anything.

**Why it matters:** Security engineers need to know if they're looking at live or stale data when deciding whether to roll out a fix.

## Findings

### UX Reviewer — friction

**Files:**
- `frontend/islands/VulnerabilityDashboard.tsx:79-85, 333` — refresh handler, lastScanned display
- `frontend/islands/VulnerabilityDetail.tsx:147-150` — detail page lastScanned display

## Proposed Solutions

### Option A: Relative time + stale warning

1. Replace `new Date(lastScanned).toLocaleString()` with a `TimeAgo`-style "2h ago" + a tooltip showing the full timestamp
2. Add a stale-data warning badge when `lastScanned` is older than a threshold (e.g., 7 days)
3. On refresh, dim the table (opacity: 0.5) until fresh data arrives

**Pros:** Clear staleness signaling; familiar pattern from other parts of the app
**Cons:** Requires a shared TimeAgo component (check if one exists; `lib/format.ts` has `age()`)
**Effort:** Small
**Risk:** None

### Option B: Minimal — add "Last updated" header

Just show relative time in the header. Skip the dim-on-refresh.

**Pros:** Trivial
**Cons:** Partial fix
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDashboard.tsx`
- `frontend/islands/VulnerabilityDetail.tsx`
- Reference: `frontend/lib/format.ts` (check for existing age/relative-time helper)

## Acceptance Criteria

- [ ] Users can see relative time of last scan
- [ ] Stale data (> threshold) is visually flagged
- [ ] Refresh has a visible effect on the data area

## Work Log

## Resources

- PR #167
