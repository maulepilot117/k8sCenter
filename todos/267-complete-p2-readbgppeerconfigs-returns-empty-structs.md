---
status: pending
priority: p2
issue_id: 267
tags: [code-review, dead-code, networking, pr-157]
dependencies: []
---

# readBGPPeerConfigs Returns Empty Structs — Dead Data

## Problem Statement

`readBGPPeerConfigs()` reads `CiliumBGPPeerConfig` CRDs but only extracts the `Name` field. The `PeerASN` and `PeerAddress` fields in `BGPPeerConfig` struct are never populated (always zero values). The comment at line 75-77 explains that ASN/address are on the ClusterConfig, not PeerConfig.

Furthermore, the frontend `BgpStatus.tsx` only renders `resp.peers` (from NodeConfig), never touching `resp.config.peerConfigs`.

## Findings

**Location:**
- `backend/internal/networking/cilium_crds.go:63-82` — reader function
- `backend/internal/networking/types.go:23-27` — struct with unpopulated fields
- `frontend/islands/BgpStatus.tsx` — never uses peerConfigs

The function and its result are dead weight in the API response.

## Proposed Solutions

### Option A: Remove readBGPPeerConfigs entirely (Recommended)
**Pros:** Less API calls, cleaner response, no misleading zero-value data
**Cons:** If Phase B needs PeerConfig, will need to re-add
**Effort:** Small
**Risk:** None

### Option B: Keep but remove empty fields from type
**Pros:** Preserves CRD listing for future use
**Cons:** Still an unnecessary API call
**Effort:** Small
**Risk:** None

## Acceptance Criteria

- [ ] No API calls to unused CRDs
- [ ] No unpopulated fields in API response
- [ ] Frontend unaffected (already ignores this data)

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | PeerConfig holds transport/timer settings, not peer identity |
