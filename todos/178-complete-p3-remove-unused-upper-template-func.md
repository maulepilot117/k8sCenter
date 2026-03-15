---
status: complete
resolution: "Already removed in a prior change."
priority: p3
issue_id: "178"
tags: [code-review, dead-code, step-11]
dependencies: []
---

# Remove Unused and Buggy `upper` Template Function

## Problem Statement
The `upper` FuncMap helper in `notifier.go:24-29` is registered but never used in any template. It also has a bug: `s[0]-32` only works for ASCII lowercase letters and produces garbage for other characters.

## Proposed Solutions
Remove the `upper` function from the FuncMap entirely. Add back with proper implementation (`strings.ToUpper`) when a template actually needs it.
- **Effort**: Small

## Resources
- PR: #17
- File: `backend/internal/alerting/notifier.go:24-29`
