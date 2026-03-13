---
status: pending
priority: p2
issue_id: "119"
tags: [code-review, frontend, step-7]
dependencies: []
---

# No File Size Check on YAML Upload

## Problem Statement

File upload in YamlApplyPage calls `file.text()` without checking `file.size`. A large file (e.g., 500MB) would be read entirely into browser memory, potentially crashing the tab before the 2MB backend limit rejects it.

## Findings

- File: `frontend/islands/YamlApplyPage.tsx` lines 94-107
- `file.text()` is called unconditionally with no size guard
- Backend enforces 2MB limit but the browser has already consumed the memory by that point

## Recommendation

Add a client-side size check before reading: `if (file.size > 2 * 1024 * 1024) { /* show error */ return; }`. This matches the backend limit and prevents unnecessary memory consumption.
