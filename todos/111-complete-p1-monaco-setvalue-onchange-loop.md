---
status: pending
priority: p1
issue_id: "111"
tags: [code-review, frontend, step-7]
dependencies: []
---

# Monaco setValue Triggers onChange Loop Causing Flicker

## Problem Statement

In YamlEditor, `editor.setValue(value)` triggers `onDidChangeModelContent`, which calls `onChange`, which sets a signal, which re-renders with a new value, which triggers the useEffect again. This creates a visible flicker on every external value update (e.g., clicking Refresh after an external change).

## Findings

- **File**: `frontend/islands/YamlEditor.tsx` lines 94-98 — `useEffect` calls `editor.setValue(value)` on value changes.
- **File**: `frontend/islands/YamlEditor.tsx` lines 130-140 — `onDidChangeModelContent` listener calls `onChange` unconditionally.

## Recommendation

Guard `onDidChangeModelContent` with an `isSettingExternally` flag (e.g., a ref) that suppresses the `onChange` callback during programmatic `setValue` calls. Set the flag to `true` before `setValue`, and reset it to `false` after.
