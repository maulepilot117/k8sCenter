---
status: complete
priority: p2
issue_id: "008"
tags: [code-review, architecture]
dependencies: []
---

# routes.go Is 410 Lines and Growing — Extract Handlers

## Problem Statement
`routes.go` contains route registration, 7 handler implementations, and utility functions. Steps 3-15 will add dozens more handlers. This file will become unmanageable.

## Findings
- **Source**: Architecture agent (Finding 2.4), Patterns agent
- **Files**: `backend/internal/server/routes.go`

## Proposed Solutions

### Option A: Domain-specific handler files (Recommended)
Split into: `routes.go` (registration only), `handle_auth.go`, `handle_cluster.go`, `handle_setup.go`, `response.go` (writeJSON helper).
- **Effort**: Medium
- **Risk**: Low — pure refactor, no behavior change

## Acceptance Criteria
- [ ] `routes.go` contains only route registration (< 50 lines)
- [ ] Handlers in domain-specific files
- [ ] `writeJSON` in shared `response.go`
- [ ] All tests pass
