---
status: complete
priority: p3
issue_id: "141"
tags: [code-review, frontend, security]
dependencies: []
---

# Detail Link Uses Unsanitized Response Data

## Problem Statement
In `WizardReviewStep.tsx`, the "View Resource" link is built from API response data without `encodeURIComponent()` on namespace and name components.

## Work Log
- 2026-03-13: Created from PR #14 code review (security-sentinel)
