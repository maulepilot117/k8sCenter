---
status: pending
priority: p3
issue_id: "123"
tags: [code-review, quality, step-7]
dependencies: []
---

# Dead Code in extractValidationMessage

## Problem Statement

`extractValidationMessage` has an if/else where both branches return `msg` unchanged. The comment says "Trim the verbose prefix" but no trimming is implemented, making the function a no-op wrapper around `err.Error()`.

## Findings

- **File:** `backend/internal/yaml/applier.go` lines 163-173
- Both the if and else branches return `msg` without modification.
- The function adds complexity with no behavioral effect.

## Recommendation

Either implement the intended prefix trimming logic, or remove the function and inline `err.Error()` at call sites.
