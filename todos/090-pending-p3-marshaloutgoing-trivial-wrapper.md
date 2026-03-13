---
status: pending
priority: p3
issue_id: "090"
tags: [code-review, backend, simplicity]
dependencies: []
---

# MarshalOutgoing Is a Trivial Wrapper

## Problem Statement
`MarshalOutgoing()` in the websocket package is a thin wrapper around `json.Marshal` that adds no logic. It can be inlined or removed to reduce indirection.

## Findings
- Function just calls `json.Marshal` and returns the result
- No error wrapping, no logging, no transformation
- Used in a few places in hub.go

## Proposed Solutions
### Option A: Inline json.Marshal at call sites, remove MarshalOutgoing
- **Effort:** Small
- **Risk:** Low
