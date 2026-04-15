---
name: Reject nonzero Size with Ed25519 algorithm
status: complete
priority: p2
issue_id: 329
tags: [code-review, backend, correctness, cert-manager, pr-180]
dependencies: []
---

## Problem Statement

`backend/internal/wizard/certificate.go:194-216` validates `Size` only for RSA and ECDSA. If a UI bug sends `Algorithm=Ed25519, Size=2048`, validation passes and YAML emits `size: 2048` under Ed25519. cert-manager will reject at apply time, but the wizard preview misleadingly shows a "valid" manifest.

Note: `ToCertificate()` at line 259-261 correctly omits size when it's zero. Default flow is fine. This catches a specific UI-bug scenario.

## Findings

- `certificate.go:194-216` (validator)
- Security review F6 (security-sentinel, P2 — correctness, not security)

## Proposed Solutions

### Option A — explicit Ed25519 branch (~3 LOC)
```go
case "Ed25519":
    if pk.Size != 0 {
        errs = append(errs, FieldError{Field: "privateKey.size", Message: "Ed25519 does not accept a size"})
    }
```

## Acceptance Criteria
- [ ] `Ed25519 + Size=2048` rejected with clear message.
- [ ] Unit test covering the case.

## Work Log
- 2026-04-14: Filed from PR #180 review.
