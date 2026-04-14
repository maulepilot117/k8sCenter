---
name: Align ToYAML trailing-newline behavior across wizards
status: complete
priority: p2
issue_id: 330
tags: [code-review, backend, consistency, pr-180]
dependencies: []
---

## Problem Statement

`backend/internal/wizard/issuer.go:343` strips trailing newlines with `strings.TrimRight(y, "\n") + "\n"`. `certificate.go:296` and every other wizard (`hpa.go:117`, `rolebinding.go:161`, `ingress.go:225`) return `sigs.k8s.io/yaml`'s raw output untouched.

## Findings

- `issuer.go:343` — trims
- `certificate.go:296` — does not
- All other wizards — do not
- Reviewer: pattern-recognition-specialist (P2)

## Proposed Solutions

### Option A — drop the trim in issuer.go (recommended)
Match the 18-wizard convention. If preview-rendering looks odd, fix the preview component instead.

## Acceptance Criteria
- [ ] `issuer.go` ToYAML matches the sibling wizards' shape exactly.
- [ ] Preview still renders without a visible trailing blank line.

## Work Log
- 2026-04-14: Filed from PR #180 review.
