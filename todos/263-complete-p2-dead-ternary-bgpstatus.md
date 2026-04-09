---
status: pending
priority: p2
issue_id: 263
tags: [code-review, dead-code, frontend, pr-157]
dependencies: []
---

# Dead Ternary in BgpStatus — Both Branches Identical

## Problem Statement

In `BgpStatus.tsx`, the StatusBadge `status` prop has a ternary where both branches produce the same string. The `allEstablished` condition has no effect.

## Findings

**Location:** `frontend/islands/BgpStatus.tsx:47-49`

```tsx
status={allEstablished
  ? `${established}/${peers.length} Established`
  : `${established}/${peers.length} Established`}  // identical!
```

The `variant` prop correctly uses `allEstablished` for coloring (success vs warning vs danger), but the text is the same regardless.

Likely intent: show different text like "All Established" vs "X/Y Established".

## Proposed Solutions

### Option A: Differentiate text (Recommended)
```tsx
status={allEstablished
  ? "All Established"
  : `${established}/${peers.length} Established`}
```
**Effort:** Small | **Risk:** None

## Acceptance Criteria

- [ ] Ternary branches produce different output
- [ ] StatusBadge text accurately reflects state

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Copy-paste error |
