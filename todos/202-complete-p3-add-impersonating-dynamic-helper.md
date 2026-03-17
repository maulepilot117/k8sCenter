---
status: pending
priority: p3
issue_id: "202"
tags: [code-review, architecture, dry]
dependencies: []
---

# Add impersonatingDynamic Helper to Reduce Boilerplate

## Problem Statement

`h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)` is repeated 4 times in cilium.go. The existing `impersonatingClient(user)` helper wraps the typed client — a matching `impersonatingDynamic(user)` would reduce duplication.

## Findings

- **cilium.go**: Lines ~89, ~131, ~157, ~202 all have identical 4-line boilerplate
- **handler.go:65-67**: `impersonatingClient` helper for typed client
- Found by: Pattern Recognition

## Acceptance Criteria
- [ ] `impersonatingDynamic(user)` helper added to handler.go
- [ ] cilium.go uses the helper

## Work Log
- 2026-03-16: Created from PR #36 code review
