---
status: pending
priority: p2
issue_id: "117"
tags: [code-review, frontend, step-7]
dependencies: []
---

# beforeunload Handler Captures Stale Closure

## Problem Statement

The `beforeunload` useEffect has empty deps `[]`, capturing `yamlContent` from the first render (which is `""` since the resource has not loaded yet). While signal reads work fine, `yamlContent` (a plain useMemo variable) is stale forever. This makes the dirty guard unreliable — it may prompt when no changes exist.

## Findings

- File: `frontend/islands/ResourceDetail.tsx` lines 74-83
- `yamlContent` is a plain variable from `useMemo`, not a signal
- The empty dependency array means the `beforeunload` handler never re-registers with the updated value

## Recommendation

Convert `yamlContent` to a signal so the `beforeunload` handler reads the current value, or add `yamlContent` to the useEffect dependency array to re-register the handler when it changes.
