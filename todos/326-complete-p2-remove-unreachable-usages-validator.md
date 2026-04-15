---
name: Remove 24-entry validCertificateUsages map (unreachable from UI)
status: complete
priority: p2
issue_id: 326
tags: [code-review, backend, yagni, pr-180]
dependencies: []
---

## Problem Statement

`backend/internal/wizard/certificate.go:31-55, 182-189` maintains a 24-entry `validCertificateUsages` map and loops through `Usages` rejecting unknown values. The frontend does not expose a way to set `Usages`. Even when set via API, cert-manager's own admission webhook validates these values.

## Findings

- `certificate.go:31-55` — 24-entry map
- `certificate.go:182-189` — validator loop
- `certificate_test.go:181-189` — test for unknown usage error
- Reviewer: code-simplicity-reviewer (P2)

## Proposed Solutions

### Option A — delete the map and the validator
Keep the `Usages` field in the struct (API callers may populate it), let cert-manager reject bad values. Saves ~30 LOC + 1 test.

### Option B — expose usages in UI
Add a multi-select to `CertificateForm.tsx`. Makes the validator earn its keep.
**Pros:** power-user capability. **Cons:** clutter for a field most users never touch.

## Recommended Action
<!-- Option A unless follow-up plans UI exposure. -->

## Acceptance Criteria
- [ ] Map + validator removed.
- [ ] `TestCertificateValidate_UnknownUsage` removed.
- [ ] `go test ./internal/wizard/...` still green.

## Work Log
- 2026-04-14: Filed from PR #180 review.
