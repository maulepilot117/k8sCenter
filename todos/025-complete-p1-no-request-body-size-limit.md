---
status: complete
priority: p1
issue_id: "025"
tags: [code-review, security, dos]
dependencies: []
---

# No Request Body Size Limit in decodeBody

## Problem Statement
`decodeBody` in `handler.go:104-107` reads the request body with no size limit. Any authenticated user can send a multi-gigabyte JSON payload to any create/update endpoint, causing the backend to allocate unbounded memory, resulting in OOM and a crash. This is a denial-of-service vulnerability exploitable by any authenticated user.

## Findings
`handler.go:104-107` uses `json.NewDecoder(r.Body).Decode(v)` with no `MaxBytesReader` wrapper. The raw `r.Body` is read without any size constraint.

Flagged by: Security Sentinel (Finding 2), Performance Oracle (Finding 8), Architecture Strategist (Finding 3).

## Proposed Solutions
### Option A: Add MaxBytesReader inside decodeBody
Wrap `r.Body` with `http.MaxBytesReader(w, r.Body, 1<<20)` (1 MB limit) at the start of the `decodeBody` function.
- **Pros:** Minimal change, fixes the issue at the point of use, 1 line addition
- **Cons:** Limit is hardcoded; different endpoints might need different limits in the future
- **Effort:** Small
- **Risk:** Low

### Option B: Add body size limit as middleware for POST/PUT/PATCH routes
Apply `http.MaxBytesReader` as middleware on all state-changing routes, making the limit configurable.
- **Pros:** Reusable, single enforcement point, configurable per-route if needed
- **Cons:** Slightly more code than Option A
- **Effort:** Small
- **Risk:** Low

## Recommended Action


## Technical Details
- **Affected files:** `backend/internal/k8s/resources/handler.go`
- **Components:** Request body parsing, all create/update API endpoints

## Acceptance Criteria
- [ ] Request with body larger than 1 MB returns HTTP 413 (Request Entity Too Large)
- [ ] Existing create/update tests still pass with normal-sized payloads
- [ ] Error response follows standard API error format

## Work Log
| Date | Action | Notes |
|------|--------|-------|
| 2026-03-12 | Created | From PR #3 code review |

## Resources
- PR: #3
