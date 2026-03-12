---
status: complete
priority: p3
issue_id: "011"
tags: [code-review, quality]
dependencies: []
---

# Remove Unused ExportUsers/ImportUsers and RevokeAllForUser

## Problem Statement
Three methods have no callers outside their own tests:
- `LocalProvider.ExportUsers()` — speculative persistence, not used
- `LocalProvider.ImportUsers()` — speculative persistence, not used
- `SessionStore.RevokeAllForUser()` — no admin force-logout feature yet

## Proposed Solutions
Remove all three methods and their tests. Re-add when actually needed.
- **Effort**: Small (~65 LOC removed)

## Acceptance Criteria
- [ ] Methods removed
- [ ] Tests removed
- [ ] Build passes
