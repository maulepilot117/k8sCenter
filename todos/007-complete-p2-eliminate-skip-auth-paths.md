---
status: complete
priority: p2
issue_id: "007"
tags: [code-review, architecture]
dependencies: []
---

# skipAuthPaths Hardcoded Map Is Fragile — Use Route Groups Instead

## Problem Statement
Auth middleware uses a hardcoded `skipAuthPaths` map. Every new public endpoint requires updating this map in a different file from where routes are defined. If they fall out of sync, endpoints silently require auth when they shouldn't (or vice versa).

## Findings
- **Source**: Architecture agent (Finding 2.3), Patterns agent
- **File**: `backend/internal/server/middleware/auth.go:13`, `backend/internal/server/server.go:74-75`

## Proposed Solutions

### Option A: Chi Route Groups (Recommended)
Restructure routes: public group (health, auth, setup) with NO auth middleware; authenticated group with auth + CSRF middleware applied at the group level.
- **Effort**: Medium
- **Risk**: Low

## Acceptance Criteria
- [ ] `skipAuthPaths` map eliminated
- [ ] Public routes defined in a group without auth middleware
- [ ] Authenticated routes defined in a group with auth + CSRF middleware
- [ ] All existing tests pass
