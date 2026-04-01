---
status: pending
priority: p2
issue_id: "258"
tags: [code-review, refactor, consistency]
dependencies: []
---

# Inline initialNamespace() Logic in 4 Files

## Problem Statement

Four island files inline the exact logic of `initialNamespace()` instead of calling the shared helper, violating DRY and risking drift if the default-namespace logic ever changes.

## Findings

All four files use `IS_BROWSER && selectedNamespace.value !== "all" ? selectedNamespace.value : "default"`, which is identical to `initialNamespace()` from `lib/hooks.ts`:

- `FlowViewer.tsx:82` — direct inline, trivial replacement
- `CiliumPolicyEditor.tsx:62` — direct inline, trivial replacement
- `SnapshotWizard.tsx:62` — wraps in fallback chain (`preselectedNs || initialNamespace()`), but inner expression is still the same
- `SchemaForm.tsx:311` — same fallback pattern; also has an inline namespace fetch that should use `useNamespaces()`

## Proposed Solutions

1. Replace inline expressions in FlowViewer and CiliumPolicyEditor with `initialNamespace()` import.
2. Replace inner expression in SnapshotWizard and SchemaForm fallback chains with `initialNamespace()`.
3. In SchemaForm, replace the inline namespace fetch with `useNamespaces()` hook.

## Technical Details

- `initialNamespace()` is exported from `frontend/lib/hooks.ts`
- `useNamespaces()` is the standard hook for fetching namespace lists
- SchemaForm already imports from `lib/hooks.ts` for other helpers

## Acceptance Criteria

- [ ] All 4 files import and call `initialNamespace()` instead of inlining the logic
- [ ] SchemaForm uses `useNamespaces()` for its namespace fetch
- [ ] No behavioral changes — existing default-namespace semantics preserved
- [ ] `deno lint` and `deno check` pass
