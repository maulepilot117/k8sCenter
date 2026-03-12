---
status: complete
priority: p2
issue_id: "009"
tags: [code-review, quality]
dependencies: []
---

# Cookie Construction and Audit Entry Duplicated Multiple Times

## Problem Statement
Refresh token cookie settings are duplicated in 3 handlers (login, refresh, logout). Audit entry construction is duplicated in 4 places with identical boilerplate (`Timestamp`, `ClusterID`, `SourceIP`).

## Findings
- **Source**: Simplicity agent, Patterns agent
- **Files**: `backend/internal/server/routes.go:223,302,328` (cookies), `routes.go:147,183,233,340` (audit)

## Proposed Solutions

### Option A: Extract helpers (Recommended)
- `func (s *Server) setRefreshCookie(w, value, maxAge)` — 5-line helper
- `func (s *Server) newAuditEntry(r, user, action, result) audit.Entry` — fills common fields
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Cookie settings defined in one place
- [ ] Audit entry common fields in one place
- [ ] No behavior change
