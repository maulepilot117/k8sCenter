---
status: pending
priority: p3
issue_id: "264"
tags: [code-review, bug, rbac]
dependencies: []
---

# Alertmanagers RBAC Check Uses Wrong API Group

## Problem Statement

`dashboard.go:128` checks `h.canList(ctx, user, "alertmanagers", "")` which resolves to the core API group instead of `monitoring.coreos.com`, causing alert counts to never appear on the dashboard even for users with full monitoring permissions.

## Findings

- `access.go`'s `apiGroupForResource` maps `alertmanagerconfigs` to `monitoring.coreos.com`, but `alertmanagers` (plural) falls through to the default empty string (core group).
- This is fail-closed: data is hidden, not leaked — so it is not a security issue.
- It is a functionality bug: users with `monitoring.coreos.com` permissions never see alert counts on the dashboard.
- Pre-existing issue, not introduced by PR #129.

## Proposed Solutions

1. Add `"alertmanagers"` to the `apiGroupForResource` map in `access.go`, mapping it to `monitoring.coreos.com`.
2. Audit the map for any other Prometheus Operator CRDs that may be missing (e.g., `prometheuses`, `podmonitors`, `servicemonitors`, `prometheusrules`, `thanosrulers`).

## Technical Details

- The full list of Prometheus Operator CRDs in `monitoring.coreos.com`:
  `alertmanagers`, `alertmanagerconfigs`, `prometheuses`, `podmonitors`, `servicemonitors`, `prometheusrules`, `probes`, `thanosrulers`, `scrapeconfigs`
- Only resources actually referenced in RBAC checks need mapping.

## Acceptance Criteria

- [ ] `alertmanagers` mapped to `monitoring.coreos.com` in `apiGroupForResource`
- [ ] Any other referenced Prometheus Operator CRDs are also correctly mapped
- [ ] Dashboard alert counts visible for users with `monitoring.coreos.com` list permissions
- [ ] `go vet` and `go test` pass
