---
status: pending
priority: p3
issue_id: 269
tags: [code-review, frontend, design, pr-157]
dependencies: []
---

# Layout Responsibility in Child Island

## Problem Statement

`CiliumSubsystems.tsx` uses `md:col-span-2` in its root `<div>` to span the full width of the parent grid. This puts layout knowledge in the child component rather than the parent grid container (`NetworkOverview.tsx`).

## Findings

**Location:**
- `frontend/islands/CiliumSubsystems.tsx:19,49` — `class="md:col-span-2"`
- `frontend/islands/NetworkOverview.tsx:63` — parent grid: `class="grid gap-4 md:grid-cols-2"`

The parent should control how children are placed in the grid. Other islands (BgpStatus, IpamStatus) don't specify grid span, which is correct.

## Proposed Solutions

### Option A: Wrap CiliumSubsystems in a col-span div in the parent
**Effort:** Small | **Risk:** None

## Acceptance Criteria

- [ ] Grid layout controlled by parent, not children

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Minor design pattern issue |
