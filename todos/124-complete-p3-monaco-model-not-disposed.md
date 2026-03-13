---
status: pending
priority: p3
issue_id: "124"
tags: [code-review, frontend, step-7]
dependencies: []
---

# Monaco Editor Model Not Disposed on Unmount

## Problem Statement

When YamlEditor unmounts, `editor.dispose()` is called but the underlying model is not explicitly disposed. Monaco models are global singletons, so repeated mount/unmount cycles (tab switches, navigation) leak models in memory.

## Findings

- **File:** `frontend/islands/YamlEditor.tsx` lines 114-119
- Only `editor.dispose()` is called in the cleanup function.
- The model created or associated with the editor persists in Monaco's global registry.

## Recommendation

Add `editor.getModel()?.dispose()` before `editor.dispose()` in the cleanup callback.
