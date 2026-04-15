---
name: Drop speculative CertificateInput fields not exposed in UI
status: complete
priority: p2
issue_id: 325
tags: [code-review, backend, yagni, cert-manager, pr-180]
dependencies: []
---

## Problem Statement

`backend/internal/wizard/certificate.go:80-87` defines `IPAddresses`, `URIs`, and `CertificatePrivateKeyInput.Encoding`. None are rendered by `CertificateForm.tsx`. The validation code paths for them (e.g., identifier-required check at line 118) are reachable only via hand-crafted API calls, which bypass the wizard's purpose.

## Findings

- `certificate.go:80-87` — `IPAddresses`, `URIs` fields
- `certificate.go:68` — `Encoding` field (full validation at `:212`, no UI picker)
- Reviewers: code-simplicity-reviewer (P1), dhh-rails-reviewer (agrees)
- Approximate deletion: ~40 LOC backend + related test cases

## Proposed Solutions

### Option A — remove the fields entirely (recommended)
Drop `IPAddresses`, `URIs`, `Encoding`. Keep `CommonName` (UI renders it), `IsCA` (UI renders the checkbox), `RotationPolicy` (UI has dropdown).
Frontend stays untouched. Backend drops ~40 LOC incl. the `ipAddresses/uris` arms of the identifier-required check (`certificate.go:118`).

### Option B — leave in place for future
Ship as-is. Accept the dead-code cost. Only if there's an imminent follow-up PR that will wire them up.

## Recommended Action
<!-- filled at triage. Option A is right unless a follow-up PR already has these wired. -->

## Technical Details
- `backend/internal/wizard/certificate.go:80-87, 118, 212, 275-277`

## Acceptance Criteria
- [ ] Fields removed from `CertificateInput` and `CertificatePrivateKeyInput`.
- [ ] Validation branches referencing them removed.
- [ ] Identifier-required check updated to cover the remaining fields (`dnsNames`, `commonName`).
- [ ] Tests still pass; remove any that test deleted fields.

## Work Log
- 2026-04-14: Filed from PR #180 review.

## Resources
- PR #180
- Reviewers: code-simplicity-reviewer, dhh-rails-reviewer
