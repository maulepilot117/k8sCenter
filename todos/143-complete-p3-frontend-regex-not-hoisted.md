---
status: complete
priority: p3
issue_id: "143"
tags: [code-review, frontend, code-quality]
dependencies: []
---

# Frontend Validation Regex Literals Not Hoisted to Module Scope

## Problem Statement
`validateStep` functions use inline regex literals re-evaluated on each call. Backend correctly declares these as package-level vars. Frontend should hoist to module-level constants for consistency.

## Work Log
- 2026-03-13: Created from PR #14 code review (performance-oracle)
