---
status: pending
priority: p3
issue_id: "260"
tags: [code-review, refactor, simplicity]
dependencies: []
---

# SelectorEntry Type Alias is Dead Weight

## Problem Statement

`wizard-types.ts:14-17` defines `SelectorEntry = LabelEntry` with a comment claiming it is "structurally identical but semantically distinct." It adds zero type safety and has only one consumer.

## Findings

- `SelectorEntry` is a plain type alias — no branded type, no discriminant, no additional fields.
- Single consumer: `ServicePortsStep.tsx`.
- The alias provides no compile-time distinction from `LabelEntry`.

## Proposed Solutions

1. Replace `SelectorEntry` usage in `ServicePortsStep.tsx` with `LabelEntry`.
2. Remove `SelectorEntry` from `wizard-types.ts`.

## Technical Details

- Search for `SelectorEntry` across all files to confirm only one consumer.
- Verify no re-exports reference the type.

## Acceptance Criteria

- [ ] `SelectorEntry` removed from `wizard-types.ts`
- [ ] `ServicePortsStep.tsx` imports and uses `LabelEntry` directly
- [ ] `deno lint` and `deno check` pass
