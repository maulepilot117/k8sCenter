---
status: pending
priority: p1
issue_id: "220"
tags: [code-review, frontend-races, security]
dependencies: []
---

# SetupWizard has no double-click guard on Create Account

## Problem Statement

The "Create Account" button calls async `createAdmin()` which sets `loading.value = true` inside the function. Between the click and the first microtask, the user can click again. The Button component's `loading` prop may be visual-only (spinner) — verify it also sets `disabled` on the underlying `<button>`. Even if it does, the `onClick` handler fires before the signal update propagates.

A double-click sends two `POST /setup/init` requests. The backend handles this (second returns 410 Gone), but the auto-login flow races between the two calls, potentially showing a flash error.

**Location:** `frontend/islands/SetupWizard.tsx` lines 45-110 (createAdmin), 299-306 (Button)

## Proposed Solutions

### Option A: Guard at top of createAdmin
- Add `if (loading.value) return;` at the top of `createAdmin()`
- Verify Button component sets `disabled={disabled || loading}` (it does — line 41 of Button.tsx)
- **Effort:** Small (1 line)
- **Risk:** None

## Acceptance Criteria
- [ ] Double-click on Create Account does not send two requests
- [ ] Button is visually disabled during loading

## Work Log
| Date | Action | Notes |
|------|--------|-------|
| 2026-03-22 | Created | Frontend races review of PR #59 |
