---
status: pending
priority: p2
issue_id: "154"
tags: [code-review, performance, step-10]
dependencies: []
---

# BaseDynamicClient Creates New Client on Every Call (No Caching)

## Problem Statement
`BaseDynamicClient()` calls `dynamic.NewForConfig(f.baseConfig)` on every invocation, unlike `BaseClientset()` which is created once and stored. Each call creates a new HTTP client with TLS setup. Under concurrent load this creates unbounded dynamic client instances.

## Findings
- **Agents**: performance-oracle (Finding 1), architecture-strategist (P2), pattern-recognition-specialist (P2), code-simplicity-reviewer (Finding 4)
- **Location**: `backend/internal/k8s/client.go:113-120`

## Proposed Solutions
Create the base dynamic client once during `ClientFactory` initialization (or lazily with `sync.Once`) and store it as a field, mirroring `baseClientset`.

- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Base dynamic client created once and cached
- [ ] `go test -race` passes

## Work Log
- 2026-03-14: Identified by 4 review agents

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
