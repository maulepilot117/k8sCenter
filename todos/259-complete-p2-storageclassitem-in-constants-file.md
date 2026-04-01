---
status: pending
priority: p2
issue_id: "259"
tags: [code-review, refactor, architecture]
dependencies: []
---

# StorageClassItem Interface Defined in wizard-constants.ts

## Problem Statement

`wizard-constants.ts:43-47` defines a TypeScript interface (`StorageClassItem`) in a file meant exclusively for constant values. Interfaces belong in `wizard-types.ts`.

## Findings

- `StorageClassItem` is an interface, not a constant — it violates the file's single responsibility.
- `wizard-types.ts` already houses similar types: `PortEntry`, `EnvVarEntry`, `LabelEntry`, `ProbeState`.
- Flagged independently by both architecture-strategist and code-simplicity-reviewer agents.

## Proposed Solutions

1. Move `StorageClassItem` interface from `wizard-constants.ts` to `wizard-types.ts`.
2. Update all import paths that reference `StorageClassItem` from `wizard-constants.ts`.

## Technical Details

- Grep for `StorageClassItem` across all `.ts` and `.tsx` files to find consumers.
- Ensure barrel re-exports (if any) are updated.

## Acceptance Criteria

- [ ] `StorageClassItem` defined in `wizard-types.ts`, removed from `wizard-constants.ts`
- [ ] All imports updated to reference new location
- [ ] `deno lint` and `deno check` pass
