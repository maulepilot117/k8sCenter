---
status: complete
resolution: "Already removed in a prior change — resolvedMessage signal does not exist in AlertBanner.tsx."
priority: p3
issue_id: "184"
tags: [code-review, dead-code, frontend, step-11]
dependencies: []
---

# Dead resolvedMessage Signal in AlertBanner

## Problem Statement
`resolvedMessage` signal is declared and conditionally rendered in `AlertBanner.tsx` but never set to a non-null value. The rendering branch is unreachable dead code.

## Proposed Solutions
Remove the signal and the dead rendering branch (~8 LOC).
- **Effort**: Small

## Resources
- PR: #17
- File: `frontend/islands/AlertBanner.tsx:15,38-44`
