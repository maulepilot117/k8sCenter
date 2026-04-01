---
status: pending
priority: p3
issue_id: "263"
tags: [code-review, refactor, theme]
dependencies: []
---

# Hardcoded rgba() in DeploymentOverview.tsx

## Problem Statement

`DeploymentOverview.tsx:48` contains a hardcoded `"0 0 6px rgba(0, 230, 118, 0.4)"` box-shadow, breaking theme compliance. All color values should use CSS custom properties.

## Findings

- The same category of hardcoded color was already fixed in `ResourceDetail.tsx` in PR #129.
- This instance was missed during that pass.
- Phase 6C (Design Normalization) explicitly eliminated all non-theme color classes — this is a regression or oversight.

## Proposed Solutions

1. Define a `--success-dim` (or equivalent) CSS custom property in the theme system if not already present.
2. Replace the hardcoded `rgba()` with `var(--success-dim)` or compose from existing theme tokens.

## Technical Details

- Check existing theme CSS for a suitable shadow/glow token.
- If no token exists, add one to all 7 theme definitions.

## Acceptance Criteria

- [ ] No hardcoded `rgba()` color values in `DeploymentOverview.tsx`
- [ ] Box-shadow uses a CSS custom property
- [ ] Visual appearance unchanged under Nexus (default) theme
- [ ] `deno lint` passes
