---
name: Cert-manager wizard P3 cleanups (bundled)
status: pending
priority: p3
issue_id: 334
tags: [code-review, cleanup, pr-180]
dependencies: []
---

## Problem Statement

Bundle of low-priority cleanups surfaced during PR #180 review. Each is small enough that a dedicated todo is overkill; together they form a single cleanup pass.

## Findings

### 1. Shared helper location
`validateEmailAddress` lives in `certificate.go:300` but is only used by `issuer.go:169`. `validateHTTPSPublicURL` lives in `issuer.go:202` and is reusable. Move both to `backend/internal/wizard/container.go` alongside `dnsLabelRegex`, or into a new `helpers.go`. (pattern-recognition-specialist P3, dhh-rails-reviewer)

### 2. Test shape drift
New tests use multiple `TestCertificateValidate_*` functions with `wantField string` (singular). Existing convention (`rolebinding_test.go`, `hpa_test.go`) uses a single top-level `TestXInputValidate` with `wantFields []string`. Consolidate. Also: `TestACMEValidate_Email` (`issuer_test.go:148`) loops without `t.Run` wrapping — minor inconsistency. (pattern-recognition-specialist P2/P3)

### 3. gofmt alignment in issuer.go
`issuer.go:51,63-66` field tag alignment is off. Run `gofmt -w internal/wizard/issuer.go`. (pattern-recognition-specialist, security-sentinel F9)

### 4. ARIA / accessibility gaps
`<label>` elements in `CertificateForm.tsx` and `IssuerFormStep.tsx` don't use `htmlFor`/`id` pairing. Vault auth radio group lacks `<fieldset>/<legend>`. Codebase-wide gap — this PR doesn't regress it, but worth tackling. (kieran-typescript-reviewer P3)

### 5. Hoist LE_PROD / LE_STAGING constants
Duplicated in `IssuerWizard.tsx:47` and `IssuerFormStep.tsx:19`. Hoist to `frontend/lib/wizard-constants.ts`. (pattern-recognition-specialist P3)

### 6. Tests of framework behavior
`TestCertificateToYAML_OmitsEmptyOptionals` (certificate_test.go:216-227) tests `sigs.k8s.io/yaml` omitempty behavior, not our logic. Drop. `TestCertificateToYAML` hardcodes 11 substrings; trim to 3-4 key ones. (code-simplicity-reviewer P3)

### 7. Email `net/mail.ParseAddress` is permissive
Accepts display-name forms like `"Foo" <a@b>`. Optional tightening: require `addr.Address == strings.TrimSpace(input)`. (security-sentinel F8)

### 8. `IssuerInput.ACME.PrivateKeySecretRefName` struct-tag alignment
Minor gofmt churn. Included in #3. (security-sentinel F9)

### 9. Confirm `dnsLabelRegex` (Go) matches `DNS_LABEL_REGEX` (TS)
`container.go:13` has `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`. `wizard-constants.ts:80` should match byte-for-byte. Add a test-level assertion or a regenerated-constant pipeline. (dhh-rails-reviewer)

### 10. ACME privateKeySecretRefName auto-default stale-baseline bug
`IssuerWizard.tsx:81-90` checks "untouched" against `form.value.name + "-account"` but only inside the `type === "acme"` branch. Toggling type away and back breaks the heuristic. Low-severity. (dhh-rails-reviewer)

## Proposed Solutions

Single cleanup PR touching the above files. Items can be dropped individually if cost/value ratio shifts.

## Acceptance Criteria

- [ ] Items 1–10 each have a resolution (done, deferred, or explicitly rejected).
- [ ] `go vet`, `go test`, `deno fmt --check`, `deno lint` all pass.

## Work Log

- 2026-04-14: Filed from PR #180 review.
