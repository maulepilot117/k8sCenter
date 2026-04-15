---
name: Consider removing CA and Vault issuer types from the wizard
status: pending
priority: p2
issue_id: 333
tags: [code-review, yagni, cert-manager, design-decision, pr-180]
dependencies: []
---

## Problem Statement

The Issuer wizard ships four backend types: SelfSigned, ACME, CA, Vault. Reviewer (code-simplicity-reviewer) argues CA and Vault should be cut:

- **CA** is literally "enter a Secret name." YAML is 5 lines. Wizard adds no value over the YAML Editor.
- **Vault** has 3 auth modes with a custom radio state machine (`VaultAuthMethod`). Vault operators typically author YAML directly or use their org's tooling.

Together ~180 LOC of Go + TSX + tests.

## Findings

- `issuer.go:55-73, 143-154, 219-253, 283-287, 316-334` (CA + Vault Go)
- `IssuerFormStep.tsx:196-324` (CA + Vault UI)
- `IssuerTypePickerStep.tsx` (4 cards would drop to 2)
- Reviewer: code-simplicity-reviewer (P1)

## Proposed Solutions

### Option A — cut both
SelfSigned + ACME only. Users who need CA/Vault author YAML directly. ~180 LOC deletion.

### Option B — keep both
Argument: the wizard exists precisely so users *don't* write YAML. The UI is already built. Cost is just ongoing maintenance, which is low since cert-manager's CA/Vault specs are stable.

### Option C — keep CA (it's trivial), drop Vault (complex for niche)
Middle ground. CA's UI is 20 LOC; Vault's is 100+.

## Recommended Action
<!-- Needs product/maintainer judgment. This is a design call, not a bug. -->

## Acceptance Criteria
- [ ] Decision made and documented.
- [ ] If cutting, `issuer.go`, `IssuerFormStep.tsx`, `IssuerTypePickerStep.tsx`, and tests updated.

## Work Log
- 2026-04-14: Filed from PR #180 review.
