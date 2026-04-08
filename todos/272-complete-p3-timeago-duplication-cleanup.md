---
status: pending
priority: p3
issue_id: "272"
tags: [code-review, quality, frontend, duplication]
dependencies: []
---

# timeAgo Utility Created But Pre-Existing Copies Not Updated

## Problem Statement

This PR correctly extracts `timeAgo()` to `lib/timeAgo.ts`, but `NamespaceTopology.tsx` (line 84) and `GitOpsAppSets.tsx` (line 26, as `formatAge()`) still have their own inline implementations with slightly different behavior.

## Findings

**Identified by:** Pattern Recognition, Code Simplicity, Architecture Strategist

## Proposed Solutions

Migrate NamespaceTopology and GitOpsAppSets to import from `lib/timeAgo.ts`. Small effort, reduces maintenance burden.

## Acceptance Criteria

- [ ] NamespaceTopology.tsx uses shared lib/timeAgo.ts
- [ ] GitOpsAppSets.tsx uses shared lib/timeAgo.ts
- [ ] Inline implementations removed

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | Pre-existing debt, but this PR created the shared version |

## Resources

- PR: #153
