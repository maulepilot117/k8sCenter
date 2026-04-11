---
status: pending
priority: p3
issue_id: "320"
tags: [code-review, pattern, consistency, pr-167]
dependencies: []
---

# VulnerabilityDetail renders loading/error inline instead of early return

## Problem Statement

`VulnerabilityDetail.tsx:154-165` renders loading and error states inline, inside the main shell, alongside conditional content blocks. `GitOpsAppDetail.tsx:204-232` uses an early-return pattern instead. Not wrong, but inconsistent.

**Why it matters:** Pattern drift makes the codebase harder to scan.

## Findings

### Pattern Recognition Reviewer

**File:** `frontend/islands/VulnerabilityDetail.tsx:154-165` vs `frontend/islands/GitOpsAppDetail.tsx:204-232`

## Proposed Solutions

### Option A: Match GitOpsAppDetail's early-return pattern

Extract loading and error into their own returns at the top of the function, before the main render tree.

**Pros:** Consistency with project convention
**Cons:** Slightly more verbose
**Effort:** Trivial
**Risk:** None

### Option B: Leave as-is

Accept minor inconsistency.

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx:154-165`

## Acceptance Criteria

- [ ] Pattern matches GitOpsAppDetail OR decision documented

## Work Log

## Resources

- PR #167
