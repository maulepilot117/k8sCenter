---
status: complete
resolution: "Already consistent — all route functions use Page suffix (AlertingPage, AlertRulesPage, AlertSettingsPage)."
priority: p3
issue_id: "183"
tags: [code-review, naming, frontend, step-11]
dependencies: []
---

# Route Page Function Naming Inconsistency

## Problem Statement
`alerting/index.tsx` uses "Page" suffix (`AlertingPage`) while `rules.tsx` and `settings.tsx` use "Route" suffix (`AlertRulesRoute`, `AlertSettingsRoute`). Monitoring routes consistently use "Page".

## Proposed Solutions
Rename to use "Page" suffix consistently: `AlertRulesPage`, `AlertSettingsPage`.
- **Effort**: Small

## Resources
- PR: #17
