---
status: complete
priority: p2
issue_id: 244
tags: [consistency, frontend, code-review, phase4b]
---

# DNS_LABEL_REGEX duplicated in RoleBindingWizard instead of importing

## Problem Statement

The RoleBindingWizard defines `DNS_LABEL_REGEX` locally instead of importing it from `@/lib/wizard-constants.ts`, which is the shared location used by ServiceWizard, DeploymentWizard, and other wizards.

## Findings

Duplicating the regex creates a maintenance risk: if the pattern is updated in `wizard-constants.ts`, the RoleBindingWizard copy will not be updated, leading to inconsistent validation behavior across wizards.

## Technical Details

- **File:** `frontend/islands/RoleBindingWizard.tsx`, line 40
- **Shared location:** `frontend/lib/wizard-constants.ts` (already exports `DNS_LABEL_REGEX`)
- Other wizards (ServiceWizard, DeploymentWizard) import from the shared location

## Acceptance Criteria

- [ ] Local `DNS_LABEL_REGEX` definition removed from `RoleBindingWizard.tsx`
- [ ] `DNS_LABEL_REGEX` imported from `@/lib/wizard-constants.ts`
- [ ] No behavioral change to validation
