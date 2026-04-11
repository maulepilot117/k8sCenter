---
status: pending
priority: p2
issue_id: "307"
tags: [code-review, ux, scanning, pr-167]
dependencies: []
---

# Kubescape rows are silently dead-ended in the dashboard

## Problem Statement

The new clickable-row behavior only applies to Trivy-scanned workloads. Kubescape rows render as plain `<tr>` with no visual distinction from Trivy rows, no cursor indicator, no tooltip, and no action. Users will click them expecting the same drill-down and nothing happens. When they click the detail link on a Kubescape workload from somewhere else (e.g., direct URL), the 501 error says "This cluster does not have Trivy installed" — misleading when Trivy may in fact be installed but this specific workload was scanned by Kubescape.

**Why it matters:** Blocks an entire class of users (Kubescape-only clusters) with no recovery path.

## Findings

### UX Reviewer — Blocker

**Files:**
- `frontend/islands/VulnerabilityDashboard.tsx:302-317` — Kubescape rows indistinguishable from Trivy rows
- `frontend/islands/VulnerabilityDetail.tsx:70-72` — 501 error message not scanner-aware

## Proposed Solutions

### Option A: Visual hint + accurate error (Recommended)

1. In `WorkloadRow`, when `href` is null (Kubescape), render with:
   - `cursor-not-allowed` cursor
   - `title` attribute: "CVE-level detail requires Trivy Operator"
   - Greyed-out name text to show it's non-actionable
2. In `VulnerabilityDetail.tsx` error handling, change the 501 message to: "CVE-level detail requires Trivy Operator. This workload was scanned by Kubescape, which does not expose per-CVE data under user impersonation."

**Pros:** Users immediately see which rows are actionable; error message is accurate when they do navigate
**Cons:** None
**Effort:** Small
**Risk:** None

### Option B: Hide Kubescape rows entirely

Filter Kubescape rows out of the dashboard since they're not actionable.

**Pros:** No dead-ends
**Cons:** Loses visibility of scanner coverage; users in Kubescape-only clusters see an empty dashboard
**Effort:** Trivial
**Risk:** Low

## Recommended Action

<!-- Filled during triage — Option A -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDashboard.tsx:387-460` (WorkloadRow non-href branch)
- `frontend/islands/VulnerabilityDetail.tsx:70-78` (error message)

## Acceptance Criteria

- [ ] Kubescape rows render with visually distinct "not clickable" state
- [ ] Hover tooltip explains why the row is not clickable
- [ ] 501 error message references Kubescape scanner specifically

## Work Log

## Resources

- PR #167
