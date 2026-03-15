---
status: complete
priority: p3
issue_id: "148"
tags: [code-review, frontend, duplication]
dependencies: []
---

# Wizard Island Lifecycle Logic Duplicated

## Problem Statement
DeploymentWizard and ServiceWizard share ~80 lines of identical logic: namespace fetching, beforeunload guard, dirty tracking, preview state, goNext/goBack navigation. Extract a shared `useWizard()` hook when a third wizard is added (Step 10).

## Work Log
- 2026-03-13: Created from PR #14 code review (code-simplicity-reviewer, architecture-strategist, pattern-recognition-specialist)
