---
status: pending
priority: p3
issue_id: "065"
tags: [code-review, frontend, duplication]
dependencies: []
---

# Duplicated Error Alert Markup — Extract Alert Component

## Problem Statement
The same error alert Tailwind class string is copy-pasted in Dashboard.tsx (line 106) and LoginForm.tsx (line 45).

## Findings
- Both use: `rounded-md bg-red-50 px-4 py-3 text-sm text-red-800 dark:bg-red-900/30 dark:text-red-400`
- Should be an `Alert` component with variant prop (error, warning, info)

Flagged by: Pattern Recognition Specialist

## Proposed Solutions

### Option A: Extract `components/ui/Alert.tsx`
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Alert component with variant prop
- [ ] Dashboard and LoginForm use the shared component
