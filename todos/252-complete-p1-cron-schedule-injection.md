---
status: complete
priority: p1
issue_id: 252
tags: [security, validation, code-review, phase4c]
---

## Problem Statement
Schedule field in ScheduledSnapshotInput only checked for empty, not format-validated. Fixed by adding 5-field cron regex.

## Technical Details
- File: backend/internal/wizard/scheduled_snapshot.go
