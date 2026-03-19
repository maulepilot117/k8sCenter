---
status: pending
priority: p3
issue_id: "215"
tags: [code-review, frontend, duplication]
dependencies: []
---

# Extract shared Toast component from UserManager and ResourceTable

## Problem Statement

Toast notification markup, signal shape `{ message, type, ts }`, and auto-dismiss
effect are duplicated between UserManager.tsx and ResourceTable.tsx. Extract when
a third consumer appears.

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found during PR #48 review — tolerable for 2 consumers |
