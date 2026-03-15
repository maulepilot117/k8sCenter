---
status: complete
resolution: "Already replaced in a prior change — parsePositiveInt and parseIntOrDefault no longer exist."
priority: p3
issue_id: "179"
tags: [code-review, simplification, step-11]
dependencies: []
---

# Replace Custom Int Parsers with strconv.Atoi

## Problem Statement
`parsePositiveInt` and `parseIntOrDefault` in handler.go reinvent `strconv.Atoi` poorly. The call site double-parses the same string. `parsePositiveInt` also has no overflow protection.

## Proposed Solutions
Replace with `strconv.Atoi` + positivity check. Remove both custom functions (~18 LOC).
- **Effort**: Small

## Resources
- PR: #17
- File: `backend/internal/alerting/handler.go:508-525,154-159`
