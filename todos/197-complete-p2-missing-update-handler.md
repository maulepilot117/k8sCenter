---
status: pending
priority: p2
issue_id: "197"
tags: [code-review, architecture, completeness]
dependencies: []
---

# Missing PUT/Update Handler for CiliumNetworkPolicy

## Problem Statement

All other mutable resource types (networkpolicies, deployments, services, configmaps, etc.) implement update handlers. CiliumNetworkPolicies only have list, get, create, delete. Users must delete and recreate to modify a policy, creating a window where no policy exists.

## Findings

- **routes.go:411-416**: No PUT route registered
- **networkpolicies.go:95-125**: Shows the update pattern to follow
- Security gap: delete-recreate window leaves pods unprotected
- Found by: Architecture, Pattern Recognition, Security reviewers

## Proposed Solutions

### Option A: Add HandleUpdateCiliumPolicy following networkpolicies.go pattern
Accept `CiliumPolicyRequest`, validate, build unstructured, call `dc.Resource().Namespace().Update()`.
- Effort: Medium
- Risk: Low

## Acceptance Criteria
- [ ] PUT endpoint registered at `/resources/ciliumnetworkpolicies/{namespace}/{name}`
- [ ] RBAC check for "update" verb
- [ ] Audit logging on update
- [ ] Atomic update (no delete-recreate window)

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- Pattern: `backend/internal/k8s/resources/networkpolicies.go:95-125`
