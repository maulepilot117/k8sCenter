---
status: pending
priority: p3
issue_id: 268
tags: [code-review, networking, pr-157]
dependencies: []
---

# IPAM Mode Hardcoded to "cluster-pool"

## Problem Statement

`HandleCiliumIPAM` always returns `mode: "cluster-pool"` when CNI info exists. The comment says "Try to read mode from ConfigMap" but the code doesn't actually do this.

## Findings

**Location:** `backend/internal/networking/handler.go:392-398`

```go
mode := "unknown"
info := h.Detector.CachedInfo()
if info != nil {
    // Try to read mode from ConfigMap — fall back to cluster-pool as default
    mode = "cluster-pool"
}
```

Cilium supports multiple IPAM modes: `cluster-pool`, `kubernetes`, `multi-pool`, `azure`, `eni`. The actual mode is in the `cilium-config` ConfigMap under key `ipam`.

## Proposed Solutions

### Option A: Read from ConfigMap via Detector's cached config
**Effort:** Small | **Risk:** Low

## Acceptance Criteria

- [ ] IPAM mode reflects actual ConfigMap value

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | TODO left in code |
