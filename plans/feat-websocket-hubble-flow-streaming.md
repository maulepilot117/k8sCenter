# feat: WebSocket Hubble Flow Streaming

Replace HTTP polling with real-time WebSocket streaming for Hubble network flows.

## Problem Statement

The Hubble flows page uses manual HTTP refresh. For debugging network issues (dropped packets, policy violations), users need to see flows as they happen.

## Proposed Solution

Add a `StreamFlows` method to `HubbleClient` and a dedicated WS endpoint (`/ws/flows`) that pipes gRPC flow data directly to the browser per-client. No shared stream abstraction — one gRPC stream per WebSocket client. gRPC multiplexes over a single HTTP/2 connection, so multiple concurrent streams are cheap.

---

## Implementation Plan

### Step A: Backend — StreamFlows + WS Handler

**`backend/internal/networking/hubble_client.go`** — add streaming method (~30 lines):

```go
func (c *HubbleClient) StreamFlows(ctx context.Context, namespace, verdict string, cb func(FlowRecord)) error
```

- Sets `Follow: true` on `GetFlowsRequest` for continuous streaming
- Applies `Whitelist` filters for namespace and verdict
- Reads from gRPC stream in a loop, converts `flow.Flow` → `FlowRecord`, calls `cb`
- Returns when context is cancelled or gRPC stream errors
- Logs stream start/stop for observability

**Create `backend/internal/server/handle_ws_flows.go`** (~80 lines):

- Upgrades HTTP to WebSocket (gorilla/websocket, reuse existing origin validation)
- Reads first message for auth (JWT token, same pattern as existing WS hub)
- Reads second message for filter: `{ "namespace": "default", "verdict": "" }`
- RBAC: use `AccessChecker.CanAccess(ctx, user, "list", "pods", namespace)` — consistent with Hub pattern, avoids unnecessary API server load from direct pod list
- Opens gRPC stream via `hubbleClient.StreamFlows(ctx, ns, verdict, cb)`
- Callback writes each flow as JSON to the WebSocket (non-blocking — skip if write fails)
- Ping/pong keepalive for dead connection detection
- On WS close or error: cancel context → gRPC stream stops
- On gRPC stream error: send error message to client, close WS

**`backend/internal/server/routes.go`** — register WS route:

```go
if s.NetworkingHandler != nil && s.NetworkingHandler.HubbleClient != nil {
    s.Router.Group(func(r chi.Router) {
        r.Use(middleware.Auth(s.TokenManager))
        r.Get("/api/v1/ws/flows", s.handleWSFlows)
    })
}
```

Route only registered when Hubble is available (same pattern as pod exec WS).

**`backend/internal/server/server.go`** — no new fields needed. The handler accesses `s.NetworkingHandler.HubbleClient` directly.

### Step B: Frontend — Replace HTTP Fetch with WS

**`frontend/islands/FlowViewer.tsx`** — modify existing component:

- On mount (or filter change): attempt WS connection to `/ws/flows`
- Send auth message, then filter message `{ namespace, verdict }`
- On flow message: prepend to flows array, trim to last 1000 if over limit
- On WS error/close: fall back to existing `fetchFlows()` HTTP call silently
- Show subtle "Live" indicator when WS is connected (no toggle — always try WS first)
- Keep existing Refresh button as manual re-fetch fallback
- On namespace/verdict change: close existing WS, open new one with new filters
- Clean up WS on component unmount

**`frontend/routes/ws/[...path].ts`** — add 1 regex to allowlist:

```
/^v1\/ws\/flows$/
```

### Step C: Tests + Verify

**`backend/internal/networking/hubble_client_test.go`** or inline tests:

- Test `StreamFlows` with a mock gRPC stream — verify callback receives flows
- Test context cancellation stops the stream
- Test filter application (namespace, verdict)

**Verification:**
- `go vet` + `go test ./... -race` pass
- Manual: open flows page, verify real-time flow display
- Manual: change namespace filter — verify stream switches
- Manual: disconnect Hubble relay — verify graceful fallback to HTTP
- Manual: on non-Hubble cluster — verify WS endpoint doesn't exist, HTTP works

---

## Acceptance Criteria

- [ ] `StreamFlows` method on HubbleClient with `Follow: true`
- [ ] WebSocket endpoint at `/api/v1/ws/flows` — only registered when Hubble available
- [ ] JWT auth + RBAC (`list pods` via AccessChecker) on WS connect
- [ ] Flows stream in real-time per-client (one gRPC stream per WS client)
- [ ] Non-blocking WS write — skip flow if client is slow
- [ ] Ping/pong keepalive for dead connection detection
- [ ] Clean shutdown: WS close → context cancel → gRPC stream stop
- [ ] Frontend: auto-stream with HTTP fallback, array trim at 1000
- [ ] gRPC stream errors logged with reconnect info
- [ ] `go test ./... -race` passes

## Files to Create/Modify

| File | Action | Purpose |
|------|--------|---------|
| `backend/internal/networking/hubble_client.go` | Modify | Add `StreamFlows` method (~30 lines) |
| `backend/internal/server/handle_ws_flows.go` | Create | WS handler for flows (~80 lines) |
| `backend/internal/server/routes.go` | Modify | Register `/ws/flows` route (3 lines) |
| `frontend/islands/FlowViewer.tsx` | Modify | Replace HTTP fetch with WS streaming |
| `frontend/routes/ws/[...path].ts` | Modify | Add 1 regex to WS proxy allowlist |

## Key Design Decisions

1. **Direct per-client gRPC stream, no shared abstraction.** gRPC multiplexes over one HTTP/2 connection. 1-3 concurrent streams are cheap. A shared FlowStreamer would add complex concurrency code for a problem that doesn't exist at current scale. Add sharing later if Hubble relay load becomes measurable.

2. **Separate WS endpoint (`/ws/flows`).** Flow protocol is continuous push with namespace filter, not the subscribe/event model used by `/ws/resources`. Different data volume, different semantics, different failure modes.

3. **No rate limiting for MVP.** The gRPC stream returns flows at the rate Hubble produces them. If a namespace is quiet, a few/sec. If noisy, the user can filter by verdict. Add server-side throttling later if frontend jank is observed.

4. **No live/polling toggle.** Always try WS first, fall back to HTTP silently. Simpler UX, less frontend state.

5. **RBAC via AccessChecker.** Consistent with Hub's subscription checks. Uses SelfSubjectAccessReview instead of direct API call.
