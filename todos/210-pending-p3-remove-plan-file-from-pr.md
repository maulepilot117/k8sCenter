---
status: pending
priority: p3
issue_id: "210"
tags: [code-review, cleanup]
dependencies: []
---

# Plan file included in PR should be moved or removed

## Problem Statement

`plans/feat-resource-action-buttons.md` (201 lines) is a development plan file
shipped with the code. These plan files are useful during development but add
noise to the repository over time.

**Location:** `plans/feat-resource-action-buttons.md`

## Proposed Solutions

### Option A: Keep in plans/ directory (current approach)
- Other plan files exist in plans/ — this is consistent
- **Effort:** None

### Option B: Remove from PR, keep only in git history
- **Effort:** Small

## Recommended Action

Option A — the plans/ directory already exists with feat-kubecenter-phase1-mvp.md.

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found during PR #46 review — low priority |
