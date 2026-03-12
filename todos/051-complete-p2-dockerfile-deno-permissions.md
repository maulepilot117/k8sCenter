---
status: complete
priority: p2
issue_id: "051"
tags: [code-review, frontend, security, docker]
dependencies: []
---

# Frontend Dockerfile Uses -A (All Permissions)

## Problem Statement
The frontend Dockerfile runs Deno with `-A` flag (all permissions) instead of explicit, minimal permission flags. This violates the principle of least privilege and Deno's security model.

## Findings
- `frontend/Dockerfile` — Uses `deno run -A` in the CMD
- Deno's security model is one of its key features; `-A` bypasses it entirely
- Should use `--allow-net`, `--allow-read`, `--allow-env` with specific scopes

Flagged by: Security Sentinel (MEDIUM)

## Proposed Solutions

### Option A: Use explicit permission flags
- **Pros**: Principle of least privilege, catches unintended access
- **Cons**: Must be kept in sync with actual needs
- **Effort**: Small
- **Risk**: Low

Use: `--allow-net=0.0.0.0:8000,${BACKEND_HOST} --allow-read=. --allow-env`

## Technical Details
- **Affected files**: `frontend/Dockerfile`

## Acceptance Criteria
- [ ] Dockerfile uses explicit Deno permissions, not -A
- [ ] Frontend still starts and functions correctly with restricted permissions
