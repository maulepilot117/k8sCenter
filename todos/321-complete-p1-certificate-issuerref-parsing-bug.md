---
name: Fix brittle issuerRef parsing in CertificateWizard
status: complete
priority: p1
issue_id: 321
tags: [code-review, frontend, correctness, cert-manager, pr-180]
dependencies: []
---

## Problem Statement

`frontend/islands/CertificateWizard.tsx:151` decodes the selected issuer from `issuerRefValue` with a destructuring split:

```ts
const [kind, issuerName] = f.issuerRefValue.split(":");
```

Two real defects:

1. If `issuerRefValue` is empty string (user never picked an issuer but validation bypass occurred), `kind` is `""` and `issuerName` is `undefined`. The POST body sends `{kind: "", name: undefined, group: "cert-manager.io"}`. The backend returns a 422 but with a confusing field-level error.
2. The split has no bounded limit. While DNS labels can't contain `:`, future changes to the encoding (e.g., adding namespace for scoped Issuers) would silently truncate.

## Findings

- `CertificateWizard.tsx:151` — `.split(":")` with positional destructure, no kind validation.
- `CertificateWizard.tsx:134-136` — `validateStep` only checks `!f.issuerRefValue`, does not assert kind/name shape.
- Reviewer: kieran-typescript-reviewer (P1).

## Proposed Solutions

### Option A — bounded indexOf + explicit kind validation (recommended)
```ts
const idx = f.issuerRefValue.indexOf(":");
const kind = idx > 0 ? f.issuerRefValue.slice(0, idx) : "";
const issuerName = idx > 0 ? f.issuerRefValue.slice(idx + 1) : "";
if (kind !== "Issuer" && kind !== "ClusterIssuer") {
  previewError.value = "Invalid issuer selection";
  previewLoading.value = false;
  return;
}
```
Also add a validation branch: `if (kind !== "Issuer" && kind !== "ClusterIssuer") errs.issuerRef = "..."` in `validateStep`.

**Pros:** robust, explicit. **Cons:** 8 more lines. **Effort:** Small. **Risk:** None.

### Option B — store as object instead of encoded string
Change `issuerRefValue` from `string` to `{kind, name} | null`. `<select>` value becomes an index or uid; map back to the issuer in the selected handler.
**Pros:** structurally correct. **Cons:** bigger refactor, touches `CertificateForm.tsx` too. **Effort:** Medium. **Risk:** Low.

## Recommended Action
<!-- filled at triage -->

## Technical Details
- `frontend/islands/CertificateWizard.tsx` (split + validateStep)
- `frontend/components/wizard/CertificateForm.tsx:27-28` (option-value encoder)

## Acceptance Criteria
- [ ] Empty `issuerRefValue` produces a field-level error and does not POST.
- [ ] `issuerRefValue` with a `kind` outside `{Issuer, ClusterIssuer}` is rejected client-side.
- [ ] Unit or integration test covering the above two cases.

## Work Log
- 2026-04-14: Filed from PR #180 review.

## Resources
- PR #180
- Reviewer report: kieran-typescript-reviewer
