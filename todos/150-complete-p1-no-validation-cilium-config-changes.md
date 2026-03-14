---
status: pending
priority: p1
issue_id: "150"
tags: [code-review, security, step-10]
dependencies: []
---

# No Input Validation or Allowlist for Cilium Config Changes

## Problem Statement
The `HandleUpdateCNIConfig` endpoint accepts an arbitrary `map[string]string` of changes and applies them directly to the `cilium-config` ConfigMap with zero validation. No allowlist of permitted keys, no value validation, no limit on number of changes, and no protection against overwriting critical keys like `tunnel`, `enable-policy`, or `identity-allocation-mode`. A user could disable network policy enforcement, tunneling, or encryption across the entire cluster.

## Findings
- **Agents**: security-sentinel (CRITICAL-02), data-integrity-guardian (P1-FINDING-3), pattern-recognition-specialist (P2), code-simplicity-reviewer (YAGNI)
- **Location**: `backend/internal/networking/handler.go:108-127`, `backend/internal/networking/cilium.go:49-72`
- **Evidence**: `CiliumConfigUpdate.Changes` is `map[string]string` with no validation. The handler decodes and passes directly to `UpdateCiliumConfig`.

## Proposed Solutions

### Option A: Implement allowlist of safe-to-modify keys with per-key value validation
- Define a map of editable Cilium config keys with acceptable value types/ranges
- Reject changes to keys not in the allowlist
- Validate value formats (boolean strings, valid enum values)
- Add key name length limit (253), value length limit (1024), max changes per request (10)
- **Pros**: Safe, prevents dangerous changes, validates input
- **Cons**: Requires maintaining allowlist as Cilium evolves
- **Effort**: Medium
- **Risk**: Low

### Option B: Block known dangerous keys + add basic bounds validation
- Blocklist critical keys (`tunnel`, `identity-allocation-mode`, `cluster-name`, etc.)
- Add key/value length limits and max changes count
- **Pros**: Simpler to implement, less maintenance
- **Cons**: Blocklist can miss newly dangerous keys
- **Effort**: Small
- **Risk**: Medium (blocklist may be incomplete)

## Recommended Action
Option A for safety. The allowlist can start small (Hubble, bandwidth manager, debug flags) and grow.

## Technical Details
- **Affected files**: `backend/internal/networking/handler.go`, new validation logic
- **Components**: CNI config update handler

## Acceptance Criteria
- [ ] Allowlist of safe-to-modify keys defined
- [ ] Reject changes to keys not in allowlist
- [ ] Key name length validated (max 253)
- [ ] Value length validated (max 1024)
- [ ] Max changes per request limited (e.g., 10)
- [ ] Tests cover validation scenarios

## Work Log
- 2026-03-14: Identified by 4 review agents

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
