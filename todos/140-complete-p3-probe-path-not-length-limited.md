---
status: complete
priority: p3
issue_id: "140"
tags: [code-review, backend, validation]
dependencies: []
---

# Probe Path Not Length-Limited

## Problem Statement
HTTP probe path is validated to start with `/` but has no length limit. Add max length (e.g., 1024 chars).

## Work Log
- 2026-03-13: Created from PR #14 code review (security-sentinel)
