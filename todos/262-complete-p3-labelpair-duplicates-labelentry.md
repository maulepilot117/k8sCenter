---
status: pending
priority: p3
issue_id: "262"
tags: [code-review, refactor, duplication]
dependencies: []
---

# LabelPair in CiliumPolicyEditor Identical to LabelEntry

## Problem Statement

`CiliumPolicyEditor.tsx:10` defines `interface LabelPair { key: string; value: string }`, which is structurally identical to `LabelEntry` from `wizard-types.ts`.

## Findings

- `LabelPair` and `LabelEntry` have the exact same shape: `{ key: string; value: string }`.
- `LabelEntry` is the canonical type used across all wizard components.
- This is a local duplicate that should be consolidated.

## Proposed Solutions

1. Remove `LabelPair` interface from `CiliumPolicyEditor.tsx`.
2. Import `LabelEntry` from `wizard-types.ts` and use it everywhere `LabelPair` was referenced.

## Technical Details

- Grep for `LabelPair` to confirm it is only used within `CiliumPolicyEditor.tsx`.
- Rename local variable types from `LabelPair` to `LabelEntry` if they differ.

## Acceptance Criteria

- [ ] `LabelPair` interface removed from `CiliumPolicyEditor.tsx`
- [ ] `LabelEntry` imported and used in its place
- [ ] `deno lint` and `deno check` pass
