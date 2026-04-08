---
status: pending
priority: p3
issue_id: "273"
tags: [code-review, security, backend, validation]
dependencies: []
---

# Defense-in-Depth Validation for Provider Fields

## Problem Statement

Several Provider/Alert/Receiver input fields lack application-level validation, relying on the K8s API server to reject invalid values. While not a vulnerability, adding validation improves error messages and reduces unnecessary API calls.

## Findings

**Identified by:** Security Sentinel

- Provider `Address` field: accepts arbitrary strings (no URL format check)
- Provider `Channel` field: no length limit
- `ProviderRef` and `SecretRef` fields: not validated against k8s name regex

## Proposed Solutions

- Validate Address starts with `http://` or `https://` and parses as URL
- Cap Channel at 512 characters
- Validate ProviderRef/SecretRef against k8sNameRegex
- **Effort:** Small
- **Risk:** Low

## Acceptance Criteria

- [ ] Address validated as URL format
- [ ] Channel capped at 512 chars
- [ ] ProviderRef/SecretRef validated against k8s name regex

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | Defense-in-depth, K8s API handles the real enforcement |

## Resources

- PR: #153
