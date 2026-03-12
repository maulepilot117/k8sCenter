---
status: complete
priority: p1
issue_id: "003"
tags: [code-review, security, performance]
dependencies: []
---

# Argon2id Memory Exhaustion Under Concurrent Login Attempts

## Problem Statement
Each Argon2id hash allocates 64 MB of memory. There is no limit on concurrent hash operations. An attacker (or legitimate load spike) sending N concurrent login requests causes N * 64 MB memory allocation. 10 concurrent requests = 640 MB. The timing-attack dummy hash on unknown users means even garbage usernames trigger the full 64 MB allocation.

With typical pod limits of 256 MB, just 4 concurrent login requests can OOM-kill the pod.

## Findings
- **Source**: Performance agent (CRITICAL-2)
- **File**: `backend/internal/auth/local.go:54-83`
- **Evidence**: `argon2Memory = 64 * 1024` (64 MB), no concurrency limit

## Proposed Solutions

### Option A: Buffered Channel Semaphore (Recommended)
Add a `chan struct{}` semaphore (capacity 2-4) to `LocalProvider`. `Authenticate` must acquire before hashing. Requests that cannot acquire within a timeout return 503.
- **Effort**: Small
- **Risk**: Low

### Option B: Reduce Memory + Increase Iterations
Lower `argon2Memory` to 32 MB and increase `argon2Time` to 3. Halves peak footprint while maintaining security.
- **Effort**: Small — but changes hash params, existing passwords won't verify

## Acceptance Criteria
- [ ] Maximum concurrent Argon2id operations is bounded (configurable, default 3)
- [ ] Requests exceeding the limit get 503 with Retry-After
- [ ] Test verifies semaphore limits concurrent hashing
