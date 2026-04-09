---
status: pending
priority: p3
issue_id: 272
tags: [code-review, type-safety, networking, pr-157]
dependencies: []
---

# Go Nil Slices Serialize as JSON `null`, TypeScript Expects `[]`

## Problem Statement

When `readBGPClusterConfigs` or `readBGPPeerConfigs` error silently (handler.go:312-319), the `clusterConfigs` and `peerConfigs` variables remain nil Go slices. JSON serializes nil slices as `null`, not `[]`. The TypeScript type expects `BGPClusterConfig[]` (not `null`), which could cause runtime errors if frontend code calls `.length` or `.map()` on null.

## Findings

**Location:**
- `handler.go:311-319` — errors logged but vars remain nil
- `handler.go:330-331` — nil slices passed to response struct
- `cilium-types.ts:8` — `config: BGPConfig` with `clusterConfigs: BGPClusterConfig[]`

Frontend doesn't currently access `config.clusterConfigs`, so this is latent. But if anyone reads this data in the future, they'd hit a null dereference.

## Proposed Solutions

### Option A: Initialize to empty slices
```go
clusterConfigs = []BGPClusterConfig{}
peerConfigs = []BGPPeerConfig{}
```
**Effort:** Small | **Risk:** None

## Acceptance Criteria

- [ ] No nil slices in API responses (use empty slices)

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Go JSON gotcha: nil slice → null, empty slice → [] |
