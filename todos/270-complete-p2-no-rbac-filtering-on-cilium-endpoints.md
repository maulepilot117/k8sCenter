---
status: pending
priority: p2
issue_id: 270
tags: [code-review, security, networking, pr-157]
dependencies: []
---

# No RBAC Filtering on Cilium CRD Endpoints

## Problem Statement

The 3 new Cilium handlers use `BaseDynamicClient()` (service account) and return all data to any authenticated user without RBAC filtering. While `BaseDynamicClient()` is an established pattern for read-only CRD listing (policy, gitops, scanning, notification handlers all do this), those packages apply post-fetch RBAC filtering before returning results. The networking handlers do not — any authenticated user can see BGP peer IPs/ASNs, node names, pod CIDRs, IPAM allocation, and encryption posture.

## Findings

**Location:**
- `handler.go:294` — `if _, ok := httputil.RequireUser(w, r)` discards user (blank identifier)
- `handler.go:309` — `h.K8sClient.BaseDynamicClient()` (BGP)
- `handler.go:356` — `h.K8sClient.BaseDynamicClient()` (IPAM)
- `handler.go:461` — `h.K8sClient.BaseDynamicClient()` (subsystems)

**Comparison with existing patterns:**
- `policy/handler.go:78` — "uses service account for full cluster visibility" + lines 360-380 RBAC-filter results by namespace
- `gitops/handler.go:113` — service account fetch + RBAC filtering in response
- Networking handlers: service account fetch + NO filtering

**Data exposed to any authenticated user:**
- BGP peer IPs and ASNs (external network topology)
- Node names, pod CIDRs (internal cluster topology)
- IPAM allocation per node
- Encryption status and key IDs

In multi-tenant clusters, namespace-scoped users should not see infrastructure data.

## Proposed Solutions

### Option A: Gate with admin-only check (Recommended for cluster-scoped CRDs)
**Pros:** Simple, appropriate for cluster-scoped infrastructure data
**Cons:** Non-admin users can't see networking status
**Effort:** Small
**Risk:** Low

### Option B: Use `DynamicClientForUser` with impersonation
**Pros:** Kubernetes RBAC enforces access
**Cons:** Cilium CRDs may not have per-user RBAC; breaks shared cache
**Effort:** Medium
**Risk:** Medium

### Option C: Accept as-is with documented limitation
**Pros:** No change needed; matches notification/storage handler pattern
**Cons:** Infrastructure data visible to all users
**Effort:** None
**Risk:** Low for single-tenant clusters

## Recommended Action

Option A for multi-tenant deployments. Option C acceptable for the homelab.

## Technical Details

- **Affected files:** `backend/internal/networking/handler.go`
- **Affected handlers:** HandleCiliumBGP, HandleCiliumIPAM, HandleCiliumSubsystems
- Flagged by: Security Sentinel, Pattern Recognition, Architecture Strategist agents

## Acceptance Criteria

- [ ] Decision made on access control model for Cilium CRD data
- [ ] Either admin gate added or limitation documented

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Other CRD handlers use BaseDynamicClient but add RBAC filtering |
