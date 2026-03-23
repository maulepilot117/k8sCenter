---
status: complete
priority: p2
issue_id: 254
tags: [error-handling, code-review, phase4c]
---

## Problem Statement
HandleGetSnapshot mapped all dynamic client errors to 404. Fixed to distinguish not-found vs other errors.

## Technical Details
- File: backend/internal/storage/handler.go
