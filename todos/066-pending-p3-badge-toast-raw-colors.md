---
status: pending
priority: p3
issue_id: "066"
tags: [code-review, frontend, consistency]
dependencies: []
---

# Badge and Toast Use Raw Tailwind Colors Instead of Theme Tokens

## Problem Statement
Badge.tsx and Toast.tsx use raw color classes (`bg-green-100`, `bg-red-100`) instead of the semantic theme tokens (`--color-success`, `--color-danger`) defined in styles.css. CLAUDE.md requires consistent use of CSS custom properties for status colors.

## Findings
- `Badge.tsx:11` — `bg-green-100 text-green-800` instead of theme tokens
- `Toast.tsx:23-31` — Same pattern
- `Button.tsx` and `TopBar.tsx` correctly use theme colors (`bg-brand`, `bg-success`)

Flagged by: Pattern Recognition Specialist

## Proposed Solutions

### Option A: Replace raw colors with theme tokens
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] All status-indicating components use theme color tokens
- [ ] No raw green/red/amber/blue color classes for status indicators
