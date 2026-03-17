---
status: pending
priority: p2
issue_id: "200"
tags: [code-review, performance, frontend]
dependencies: []
---

# Debounce YAML Preview and Skip When Hidden

## Problem Statement

The `useEffect` that generates YAML preview fires on every signal change (every keystroke). When the preview panel is collapsed, YAML is still regenerated wastefully.

## Findings

- **CiliumPolicyEditor.tsx:63-74**: `useEffect` with `buildPolicyYaml` on every change
- `buildPolicyYaml` does string concatenation with array filtering on every keystroke
- When `showYaml.value === false`, the string is built but never rendered
- Found by: Performance Oracle

## Proposed Solutions

### Option A: Debounce + skip when hidden
```typescript
useEffect(() => {
  if (!showYaml.value) return;
  const timer = setTimeout(() => {
    yamlPreview.value = buildPolicyYaml(...);
  }, 150);
  return () => clearTimeout(timer);
}, [name.value, namespace.value, endpointSelector.value, rules.value, showYaml.value]);
```
- Effort: Small
- Risk: Low

## Acceptance Criteria
- [ ] YAML preview debounced (150ms)
- [ ] No YAML generation when preview is collapsed
- [ ] YAML regenerates immediately when panel is expanded

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- File: `frontend/islands/CiliumPolicyEditor.tsx:63-74`
