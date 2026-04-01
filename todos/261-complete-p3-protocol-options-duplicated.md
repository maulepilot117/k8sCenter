---
status: pending
priority: p3
issue_id: "261"
tags: [code-review, refactor, duplication]
dependencies: []
---

# PROTOCOL_OPTIONS Duplicated in 3 Files

## Problem Statement

The identical `[{ value: "TCP", label: "TCP" }, { value: "UDP", label: "UDP" }]` array is defined independently in three files instead of being shared from `wizard-constants.ts`.

## Findings

Duplicate definitions at:
- `DeploymentNetworkStep.tsx:26`
- `ServicePortsStep.tsx:23`
- `ContainerForm.tsx:21`

## Proposed Solutions

1. Add `PROTOCOL_OPTIONS` to `wizard-constants.ts`.
2. Replace the three inline definitions with imports from `wizard-constants.ts`.

## Technical Details

- The constant is a simple array of `{ value: string; label: string }` objects.
- May want to include `"SCTP"` in the future — centralizing makes that a one-line change.

## Acceptance Criteria

- [ ] `PROTOCOL_OPTIONS` exported from `wizard-constants.ts`
- [ ] All three files import and use the shared constant
- [ ] Inline definitions removed
- [ ] `deno lint` and `deno check` pass
