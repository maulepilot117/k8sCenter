# Step 20: Pod Logs & Exec

## Overview

Add pod log viewing and interactive terminal to k8sCenter. **Ship in phases** based on reviewer feedback.

## Phase A: Pod Logs (ship now)

### Backend

**`GET /api/v1/resources/pods/{ns}/{name}/logs`** — Return last N lines as JSON

Query params:
- `container` — container name (required if multi-container)
- `tailLines` — number of lines (default: 500)
- `previous` — `true` for previous container instance (default: false)
- `timestamps` — `true` to include timestamps (default: true)

Implementation:
- Use `clientset.CoreV1().Pods(ns).GetLogs(name, opts).Stream(ctx)` with impersonated client
- RBAC check against `get` on `pods/log` subresource
- Return `{"data": {"lines": ["line1", "line2", ...], "container": "nginx"}}`
- No streaming, no SSE — just a GET that returns text

**File:** `backend/internal/k8s/resources/pods.go` — add `HandlePodLogs`

### Frontend

**`frontend/islands/LogViewer.tsx`** — Log viewer island (~150 LOC)
- Container selector dropdown (populated from pod spec)
- Previous container toggle
- Auto-refresh toggle (5s interval via setInterval)
- Monospace `<pre>` with dark background, auto-scroll
- Refresh button

**No:** ANSI colors, search/filter, download, tail selector. Use browser Cmd+F for search.

### Route

Add `GET /api/v1/resources/pods/{namespace}/{name}/logs` to routes.go.
Add "Logs" tab to pod detail view in ResourceDetail.tsx.

### Acceptance Criteria
- [ ] View last 500 lines of pod logs
- [ ] Container selector for multi-container pods
- [ ] Previous container logs toggle
- [ ] Auto-refresh (5s) with toggle
- [ ] Works through BFF proxy
- [ ] RBAC checked (pods/log subresource)

---

## Phase B: WebSocket Follow Mode (ship next)

- Add WebSocket-based log streaming using existing WS infrastructure
- New subscription kind `"logs"` in the hub
- Backend opens `Follow: true` log stream and pipes lines
- Frontend LogViewer switches from polling to WS when follow is enabled

---

## Phase C: Pod Exec (ship separately)

- `WS /api/v1/ws/exec/{ns}/{pod}/{container}`
- Use `remotecommand.NewWebSocketExecutor` (NOT SPDY — available in client-go v0.35.2)
- Add `ConfigForUser()` to ClientFactory (returns rest.Config with impersonation)
- RBAC check: `create` on `pods/exec` subresource
- Separate package: `backend/internal/exec/handler.go`
- xterm.js via `npm:@xterm/xterm` (not esm.sh)
- Session timeout (30 min idle, configurable)
- Per-user concurrency limit (max 5 sessions)
- Audit: `exec_start` and `exec_end` actions
- Hardcode `/bin/sh` (no shell selector)

---

## References

- Reviewer feedback: DHH (ship logs first), Kieran (WebSocketExecutor, RBAC), Simplicity (cut 60%)
- Existing pod handlers: `backend/internal/k8s/resources/pods.go`
- Existing WS proxy: `frontend/routes/ws/[...path].ts`
