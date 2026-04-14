---
name: Replace free-text namespace inputs with useNamespaces dropdown
status: complete
priority: p2
issue_id: 324
tags: [code-review, frontend, ux, consistency, pr-180]
dependencies: []
---

## Problem Statement

`frontend/components/wizard/CertificateForm.tsx:56-67` and `IssuerFormStep.tsx:54-64` use free-text `<input>` for namespace. Existing wizards (`DeploymentBasicsStep.tsx`, etc.) use `useNamespaces()` hook + `<select>`. Free-text lets users pass client-side validation with namespaces that don't exist; they only find out at apply time.

Also: `CertificateWizard.tsx:38` hardcodes `namespace: "default"`. Other wizards use `initialNamespace()` from `lib/namespace.ts` which reads the active namespace context.

## Findings

- `CertificateForm.tsx:56-67` — free-text input
- `IssuerFormStep.tsx:54-64` — free-text input
- `CertificateWizard.tsx:38`, `IssuerWizard.tsx:53` — hardcoded `"default"` instead of `initialNamespace()`
- Reviewer: pattern-recognition-specialist (P1 on consistency)

## Proposed Solutions

### Option A — adopt existing hook + default (recommended)
Reuse `useNamespaces()` and `initialNamespace()`. Match `DeploymentBasicsStep` shape.
**Effort:** Small. **Risk:** Low — pure consistency win.

## Acceptance Criteria
- [ ] Both wizards use a namespace `<select>` populated by `useNamespaces()`.
- [ ] Both default to the active namespace via `initialNamespace()`.

## Work Log
- 2026-04-14: Filed from PR #180 review.

## Resources
- PR #180
- `frontend/islands/DeploymentWizard.tsx:6,75` (reference)
