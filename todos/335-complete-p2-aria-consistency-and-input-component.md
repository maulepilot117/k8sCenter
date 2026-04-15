---
name: ARIA inconsistency in cert-manager forms; extend Input component
status: complete
priority: p2
issue_id: 335
tags: [code-review, frontend, accessibility, simplicity, pr-181]
dependencies: []
---

## Problem Statement

PR #181 added `htmlFor`/`id`/`aria-invalid`/`aria-describedby` pairing to the Name, Secret Name, and Issuer fields in `CertificateForm.tsx` and to the Name, ACME server, ACME email, ACME private-key-secret, ACME ingress-class fields in `IssuerFormStep.tsx`. But the same `CertificateForm.tsx` skipped DNS Names, Common Name, Duration, Renew Before, Algorithm, Key Size, and Rotation — even though `errors.dnsNames`, `errors.duration`, `errors.renewBefore` are rendered. Inconsistent within one file, and the verbose 8-lines-per-input pattern is now copy-pasted across two files.

A shared `<Input>` component already exists at `frontend/components/ui/Input.tsx` but does not yet wire `aria-invalid`/`aria-describedby` itself.

## Findings

- `CertificateForm.tsx:153-188, 201-240` — fields without ARIA pairing
- `frontend/components/ui/Input.tsx:22-30` — has `label` and `error` but no ARIA attributes
- Reviewers: kieran-typescript-reviewer (P3), pattern-recognition-specialist (P3), code-simplicity-reviewer (calls this a "regression-in-disguise" — locks in copy-paste boilerplate)

## Proposed Solutions

### Option A — extend Input.tsx, migrate fields (recommended)
Add `aria-invalid` and `aria-describedby` (with auto-generated id) to `Input.tsx`. Add equivalent `Select.tsx` if it doesn't exist. Migrate `CertificateForm` and `IssuerFormStep` to use them — the wizard files become much shorter.

**Pros:** systematic; one fix benefits every wizard. **Cons:** touches more files. **Effort:** Medium.

### Option B — apply pattern uniformly in cert-manager forms only
Add the htmlFor/id/aria triplet to the remaining CertificateForm fields. Faster, but perpetuates the boilerplate.

**Pros:** small. **Cons:** establishes the wrong precedent. **Effort:** Small.

## Acceptance Criteria
- [ ] `Input.tsx` (and a `Select.tsx` if added) emit correct ARIA attributes when `error` prop is set.
- [ ] `CertificateForm.tsx` and `IssuerFormStep.tsx` use the shared component for all fields.
- [ ] No copy-pasted ARIA boilerplate in either wizard form.
- [ ] Manual screen-reader spot check still announces validation errors.

## Work Log
- 2026-04-15: Filed from PR #181 review.
