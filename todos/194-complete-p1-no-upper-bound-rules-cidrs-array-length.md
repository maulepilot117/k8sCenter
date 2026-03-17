---
status: pending
priority: p1
issue_id: "194"
tags: [code-review, security, validation]
dependencies: []
---

# No Upper Bound on Rules/CIDRs Array Length

## Problem Statement

`validateCiliumPolicy` enforces a 20-label limit on endpoint selectors, but has no bounds on ingress/egress rules count, CIDRs per rule, ports per rule, or entities per rule. An authenticated user could submit thousands of rules within the 1MB body limit, causing CPU-intensive validation and memory amplification.

## Findings

- **cilium.go:237-265** (`validateCiliumPolicy`): No length checks on `IngressRules`, `EgressRules`
- **cilium.go:267-309** (`validateRule`): No length checks on `CIDRs`, `Ports`, `Entities`, `Labels`
- Each rule minimum ~50 bytes JSON = ~20,000 rules in 1MB body
- `net.ParseCIDR` called per CIDR = O(rules * CIDRs) CPU
- Found by: Security Sentinel

## Proposed Solutions

### Option A: Add bounds in validateCiliumPolicy and validateRule
```go
if len(req.IngressRules) + len(req.EgressRules) > 100 { ... }
// In validateRule:
if len(rule.CIDRs) > 50 { ... }
if len(rule.Ports) > 100 { ... }
if len(rule.Labels) > 20 { ... }
```
- Pros: Simple, prevents abuse
- Cons: Arbitrary limits
- Effort: Small
- Risk: Low

## Acceptance Criteria
- [ ] Total rules capped (e.g., 100)
- [ ] CIDRs per rule capped (e.g., 50)
- [ ] Ports per rule capped (e.g., 100)
- [ ] Labels per rule capped (e.g., 20)
- [ ] Clear error messages returned for exceeded limits

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- File: `backend/internal/k8s/resources/cilium.go:237-309`
