---
status: pending
priority: p3
issue_id: "205"
tags: [code-review, cleanup]
dependencies: []
---

# Miscellaneous Cleanup Items

## Problem Statement

Several minor inconsistencies and cleanup items found across the PR.

## Findings

1. **Route naming inconsistency**: `/networking/cilium-policies` (kebab) vs `/networking/networkpolicies` (no separator). Minor but inconsistent.
2. **`ciliumclusterwidenetworkpolicies` in access.go**: Registered in API group mapping but no handler exists. Should add a comment explaining this is forward-looking.
3. **Tailwind color palette**: CiliumPolicyEditor uses `text-gray-*` while ServiceWizard uses `text-slate-*`.
4. **Module-level mutable `nextRuleId`**: Works but a `useRef` would be more idiomatic.
5. **No `CiliumNetworkPolicy` type in k8s-types.ts**: Inline type assertions work but are verbose.

## Acceptance Criteria
- [ ] Add comment on `ciliumclusterwidenetworkpolicies` in access.go
- [ ] Evaluate other items during triage

## Work Log
- 2026-03-16: Created from PR #36 code review
