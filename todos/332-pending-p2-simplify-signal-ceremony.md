---
name: Collapse signals/useCallback ceremony in wizard islands
status: pending
priority: p2
issue_id: 332
tags: [code-review, frontend, simplicity, pr-180]
dependencies: []
---

## Problem Statement

`CertificateWizard.tsx` has 9 `useSignal` calls + 2 `useCallback` wrappers. `IssuerWizard.tsx` has 7 signals + 6 callbacks. None of the callbacks are passed to memoized children; the signals-per-field split buys nothing for a wizard that renders on every keystroke anyway. DHH review flagged as "cargo cult."

## Findings

- `CertificateWizard.tsx:58-68, 91-114`
- `IssuerWizard.tsx:66-73, 77-132`
- Reviewer: dhh-rails-reviewer (P2 — ceremony without payoff)

## Proposed Solutions

### Option A — single combined signal
```ts
const state = useSignal({ step: 0, form: initialForm(), errors: {}, preview: {yaml: "", loading: false, error: null} });
// plain function updates: updateField, updatePrivateKey, etc. no useCallback.
```

### Option B — leave as-is
Matches `PolicyWizard.tsx` pattern. Consistency > micro-simplification.

## Recommended Action
<!-- Option B if pattern consistency wins; Option A if we want to lead. Likely B, followup refactor across all wizards. -->

## Acceptance Criteria
- [ ] Refactor applied consistently if chosen.
- [ ] Wizards still pass their tests and visual QA.

## Work Log
- 2026-04-14: Filed from PR #180 review.
