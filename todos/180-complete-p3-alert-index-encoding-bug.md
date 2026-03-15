---
status: complete
resolution: "Already fixed in a prior change — handler.go uses strconv.Itoa(i)."
priority: p3
issue_id: "180"
tags: [code-review, bug, step-11]
dependencies: []
---

# Alert Index Encoding Bug for Indices >= 10

## Problem Statement
`string(rune('0'+i))` at handler.go:80 only works for i in 0-9. For i=10+, it produces unexpected Unicode characters (`:`, `;`, etc.) instead of "10", "11".

## Proposed Solutions
Use `strconv.Itoa(i)` instead.
- **Effort**: Small (1 line)

## Resources
- PR: #17
- File: `backend/internal/alerting/handler.go:80`
