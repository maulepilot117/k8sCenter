---
status: pending
priority: p2
issue_id: "229"
tags: [code-review, backend, exec, reliability]
dependencies: []
---

# Shell Detection Shared Stdin Pipe

## Problem Statement

The shell detection cascade (trying `/bin/bash`, `/bin/sh`, etc.) shares one stdin pipe across all attempts. If the first shell fails after consuming stdin data, that data is lost for subsequent shell attempts. Additionally, the 1-second sequential timeout per shell means a worst case of 3 seconds before a working shell is found, degrading user experience.

**Location:** `backend/internal/k8s/resources/pods.go` lines 244-301

## Proposed Solutions

### Option A: Create fresh pipe per attempt
- Create a new stdin pipe for each shell detection attempt so that no data is lost between attempts.
- **Effort:** Medium — need to manage pipe lifecycle per attempt.
- **Risk:** Low.

### Option B: Detect shell via preliminary exec
- Run a quick non-interactive `exec` (e.g., `which bash || which sh`) to detect the available shell before opening the interactive session. This avoids the cascade entirely.
- **Effort:** Medium — additional API call, but simpler overall logic.
- **Risk:** Low — one extra API call adds minor latency but is more reliable.

### Option C: Parallel shell detection
- Try all shells in parallel, use the first one that succeeds, cancel the rest.
- **Effort:** Medium-High — more complex goroutine management.
- **Risk:** Medium — multiple concurrent exec sessions may have side effects.

## Acceptance Criteria

- [ ] Shell detection does not lose stdin data between attempts
- [ ] Worst-case detection time is reduced from 3s
- [ ] The first available shell is reliably detected
- [ ] Interactive session works correctly once shell is detected

## Work Log

- 2026-03-22: Created from Phase 4A code review.
