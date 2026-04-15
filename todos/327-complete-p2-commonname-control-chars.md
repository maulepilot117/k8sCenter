---
name: Reject control characters in Certificate commonName
status: complete
priority: p2
issue_id: 327
tags: [code-review, security, backend, pr-180]
dependencies: []
---

## Problem Statement

`backend/internal/wizard/certificate.go:143-145` validates only that `commonName` ≤64 chars. Values like `"CN with\nnewline"` or embedded null bytes pass and end up in the generated x509 certificate. Downstream: log aggregators, monitoring dashboards, and naive TLS clients can mis-parse. Enables log-injection into cert-manager logs and the k8sCenter audit log at Apply time.

## Findings

- `certificate.go:143-145` — only length check, no charset restriction
- Security review F7 (security-sentinel, P2)
- YAML output itself is safe (sigs.k8s.io/yaml quotes control chars), so this is not YAML injection — it's log/downstream integrity.

## Proposed Solutions

### Option A — reject bytes < 0x20 and 0x7f
Simple loop, ~5 LOC:
```go
for _, r := range c.CommonName {
    if r < 0x20 || r == 0x7f {
        errs = append(errs, FieldError{Field: "commonName", Message: "must not contain control characters"})
        break
    }
}
```

### Option B — conservative regex
`^[\x20-\x7e\p{L}\p{N}\p{P}\p{Zs}]+$` — allows printable ASCII + Unicode letters/numbers/punctuation/spaces.
**Pros:** tighter. **Cons:** may reject legitimate multi-byte patterns.

## Recommended Action
<!-- Option A is enough; Option B if we want belt-and-suspenders. -->

## Acceptance Criteria
- [ ] Control chars in `commonName` rejected at preview time.
- [ ] Test coverage for `\n`, `\0`, `\x7f`.

## Work Log
- 2026-04-14: Filed from PR #180 review.
