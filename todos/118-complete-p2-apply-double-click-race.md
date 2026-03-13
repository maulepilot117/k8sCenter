---
status: pending
priority: p2
issue_id: "118"
tags: [code-review, frontend, step-7]
dependencies: []
---

# Apply Button Vulnerable to Double-Click Race

## Problem Statement

The Apply button onClick sets `yamlApplying.value = true` but signal updates are batched — the button stays enabled in the DOM between the assignment and re-render. A fast double-click fires two concurrent `apiPostRaw` calls. The same issue exists on YamlApplyPage where validate and apply can fire concurrently.

## Findings

- File: `frontend/islands/ResourceDetail.tsx` lines 421-441
- File: `frontend/islands/YamlApplyPage.tsx` lines 59-92
- Signal assignment does not synchronously disable the button before the next click event fires

## Recommendation

Add a synchronous guard at the start of each handler: `if (yamlApplying.value) return;` (or the equivalent for validate). This prevents the second invocation regardless of render timing.
