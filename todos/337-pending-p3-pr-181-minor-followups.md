---
name: PR #181 P3 minor cleanups (bundled)
status: pending
priority: p3
issue_id: 337
tags: [code-review, cleanup, pr-181]
dependencies: []
---

## Problem Statement

Bundle of low-priority observations from PR #181 review. Each is small enough that a dedicated todo would be overkill.

## Findings

### 1. TS-side regex parity test missing
`backend/internal/wizard/regex_parity_test.go` pins the Go `dnsLabelRegex` to a string literal. Catches Go-side drift. A symmetric Deno test pinning `DNS_LABEL_REGEX.source` would close the loop on TS-side edits. Currently a one-sided tripwire. (code-simplicity-reviewer)

### 2. secretName cleared when name cleared
`CertificateWizard.tsx:113-118` — clearing `name` sets `secretName = ""`, then `validateStep` flags both as invalid. Surprising UX. Suggested fix: only auto-derive on non-empty name; leave the prior value intact otherwise. Consider adopting the `touched`-flag pattern from the issuer wizard for consistency. (kieran-typescript-reviewer)

### 3. selectType resets ACME on every selection
`IssuerWizard.tsx:102-105` resets `acme` to `initialAcme()` even when re-selecting the already-active type. Harmless but mildly surprising. Skip if `t === form.value.type`. (dhh-rails-reviewer)

### 4. Backport useCallback removal
24 other wizard islands still use `useCallback` wrappers identified as cargo cult by the DHH review. Acceptable local drift in PR #181, but worth a follow-up sweep — batch removal in one PR, not piecemeal. (pattern-recognition-specialist)

### 5. `privateKeySecretRefNameTouched` placement
Reviewers split: DHH endorsed it as the canonical "touched" pattern. Kieran flagged mixing UI state into the form payload as a smell, suggested a separate `useSignal` or a `meta:` namespace excluded from serialization. The current `fetchPreview` cherry-picks fields so the leak is hidden today, but it's a footgun for the next dev who does `{ ...f.acme }`. Worth a small cleanup if we touch this code again.

### 6. Auto-default heuristic itself
Simplicity reviewer asks: is the auto-default `<name>-account` even worth keeping? Could delete the heuristic and the touched flag in one swoop. Marginal value, modest cost.

### 7. Extend regex parity table
If we add the TS-side test (item #1), make both files table-driven over the 10+ shared regexes (`container.go:13,16`, `ingress.go:15`, etc.) rather than test-per-regex.

## Proposed Solutions
Address as part of any future cert-manager wizard work, or as a single bundled cleanup PR.

## Acceptance Criteria
- [ ] Each item has a resolution (done, deferred, or rejected).
- [ ] Related tests still pass.

## Work Log
- 2026-04-15: Filed from PR #181 review.
