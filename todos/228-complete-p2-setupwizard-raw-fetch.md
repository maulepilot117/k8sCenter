---
status: complete
priority: p2
issue_id: "228"
tags: [code-review, frontend, auth, setupwizard, dead-code]
dependencies: []
---

# SetupWizard Raw Fetch and SettingsPage Dead Code

## Problem Statement

SetupWizard uses raw `fetch()` with manual auth headers for post-login settings calls instead of `apiPut()`. This bypasses the 401-auto-refresh logic in `lib/api.ts`, meaning if the access token expires during the setup wizard flow, the settings save will fail silently or with an unhelpful error. Additionally, the `dirtyGeneral` signal in SettingsPage is dead code — declared but never used.

**Location:** `frontend/islands/SetupWizard.tsx` lines 123-176 (raw fetch), `frontend/islands/SettingsPage.tsx` line 30 (dead code)

## Proposed Solutions

### Option A: Replace raw fetch with apiPut, remove dead code
- Replace all raw `fetch()` calls in SetupWizard with `apiPut()` from `lib/api.ts` for authenticated endpoints. Remove the unused `dirtyGeneral` signal from SettingsPage.
- **Effort:** Low — swap fetch calls, remove one line.
- **Risk:** Low — apiPut handles the same logic with auto-refresh.

## Acceptance Criteria

- [ ] All authenticated API calls in SetupWizard use `apiPut()` (or appropriate api wrapper)
- [ ] 401 auto-refresh works during the setup wizard flow
- [ ] `dirtyGeneral` dead code is removed from SettingsPage
- [ ] Setup wizard flow continues to work end-to-end

## Work Log

- 2026-03-22: Created from Phase 4A code review.
