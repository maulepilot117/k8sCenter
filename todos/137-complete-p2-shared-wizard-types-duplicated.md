---
status: complete
priority: p2
issue_id: "137"
tags: [code-review, frontend, duplication]
dependencies: []
---

# ProbeState and LabelEntry Types Duplicated Across Files

## Problem Statement
`ProbeState` is defined identically in `DeploymentWizard.tsx` and `DeploymentResourcesStep.tsx`. `LabelEntry` is identical in `DeploymentBasicsStep.tsx` and `ServiceBasicsStep.tsx`. `SelectorEntry` in `ServicePortsStep.tsx` is structurally identical to `LabelEntry`. These should be shared to prevent drift.

## Proposed Solutions

### Option A: Create shared types file
- Create `frontend/lib/wizard-types.ts` with `ProbeState`, `LabelEntry`, `SelectorEntry`
- Import from all consumers
- **Effort:** Small (15 min)
- **Risk:** None

## Acceptance Criteria
- [ ] Shared types file created
- [ ] All wizard components import from shared file
- [ ] No duplicate type definitions

## Work Log
- 2026-03-13: Created from PR #14 code review

## Resources
- PR: #14 | Agents: code-simplicity-reviewer, pattern-recognition-specialist
