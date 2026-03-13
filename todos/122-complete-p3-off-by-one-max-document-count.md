---
status: pending
priority: p3
issue_id: "122"
tags: [code-review, quality, step-7]
dependencies: []
---

# Off-by-One in MaxDocumentCount Check

## Problem Statement

`ParseMultiDoc` checks `len(objects) > MaxDocumentCount` after appending, which allows 101 documents when the limit is 100. The check should use `>=` to enforce the limit correctly.

## Findings

- **File:** `backend/internal/yaml/parser.go` lines 52-54
- The append happens before the length check, so one extra document slips through before the error is raised.

## Recommendation

Change the condition from `len(objects) > MaxDocumentCount` to `len(objects) >= MaxDocumentCount`, or check before appending.
