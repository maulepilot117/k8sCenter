---
status: pending
priority: p3
issue_id: "204"
tags: [code-review, naming]
dependencies: []
---

# Prefix Exported Type Names to Avoid Collision

## Problem Statement

`PolicyRule` and `PortRule` are exported types with very generic names in the `resources` package. If another handler needs similar types, there would be a name collision.

## Findings

- **cilium.go:38-50**: `PolicyRule`, `PortRule` exported
- Should be `CiliumPolicyRule`, `CiliumPortRule`
- Also `CiliumPolicyRequest` is already prefixed — inconsistent
- Found by: Pattern Recognition

## Acceptance Criteria
- [ ] Types renamed to `CiliumPolicyRule`, `CiliumPortRule`

## Work Log
- 2026-03-16: Created from PR #36 code review
