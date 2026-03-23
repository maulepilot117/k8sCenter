---
status: complete
priority: p1
issue_id: 253
tags: [security, validation, code-review, phase4c]
---

## Problem Statement
HandleCreateSnapshot name/sourcePVC fields checked only for emptiness, not DNS label format. Fixed by adding k8sNameRegexp validation.

## Technical Details
- File: backend/internal/storage/handler.go
