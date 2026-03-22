---
status: pending
priority: p2
issue_id: "222"
tags: [code-review, ssrf, security, backend, settings]
dependencies: []
---

# Settings URL Validation — SSRF Prevention

## Problem Statement

`handleUpdateAppSettings` stores URLs (monitoring, Grafana endpoints) without validation. An admin could point monitoring URLs at internal services such as `169.254.169.254` (cloud metadata), `localhost:6443` (API server), or other private network addresses, enabling server-side request forgery (SSRF) when the backend later fetches from those URLs.

**Location:** `backend/internal/server/handle_settings.go` lines 115-155

## Proposed Solutions

### Option A: Validate scheme and reject private/loopback IPs
- Parse each URL, enforce `http://` or `https://` scheme only. Resolve the hostname and reject if the IP falls in private (RFC 1918), loopback (127.0.0.0/8), link-local (169.254.0.0/16), or IPv6 equivalents.
- **Effort:** Low — straightforward net.LookupHost + IP range check.
- **Risk:** Low — standard SSRF mitigation pattern.

### Option B: URL allowlist by pattern
- Allow only URLs matching configured domain patterns (e.g., `*.cluster.local`, specific hostnames).
- **Effort:** Medium — requires additional configuration surface.
- **Risk:** Low — more restrictive but harder to configure.

## Acceptance Criteria

- [ ] URLs must have `http` or `https` scheme; other schemes are rejected
- [ ] URLs resolving to loopback (127.0.0.0/8, ::1) are rejected
- [ ] URLs resolving to link-local (169.254.0.0/16) are rejected
- [ ] URLs resolving to private ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) are rejected or flagged with warning
- [ ] Appropriate error message returned to the client on validation failure
- [ ] Existing valid URLs continue to work

## Work Log

- 2026-03-22: Created from Phase 4A code review.
