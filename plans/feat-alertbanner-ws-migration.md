# feat: AlertBanner WebSocket Migration

Replace 30-second HTTP polling in AlertBanner with WebSocket subscription. Frontend-only change — all backend infrastructure is already in place.

## Problem Statement

`AlertBanner.tsx` uses `setInterval(fetchAlerts, 30_000)` to poll `GET /api/v1/alerts`. This means:
- Up to 30-second delay before new alerts appear
- Unnecessary HTTP requests when no alerts change
- Inconsistent with the rest of the platform (ResourceTable uses WS)

The WebSocket Hub already broadcasts alerts (`kind: "alerts"`) when the Alertmanager webhook fires. The `"alerts"` kind is in `allowedKinds` and `alwaysAllowKinds` (JWT auth only, no RBAC check needed). The `subscribe()` function in `lib/ws.ts` is ready to use.

## Implementation

**`frontend/islands/AlertBanner.tsx`** — single file change:

1. Replace `setInterval` with `subscribe("alertbanner", "alerts", "", onEvent)` from `lib/ws.ts`
2. Keep initial REST fetch on mount (`GET /api/v1/alerts`) for current state
3. On `EVENT_ADDED`: append alert to local state
4. On `EVENT_DELETED`: remove alert by fingerprint (resolved)
5. On `EVENT_RESYNC`: re-fetch via REST
6. Fall back to polling if WS unavailable (check `IS_BROWSER`)
7. Unsubscribe on unmount

## Acceptance Criteria

- [ ] AlertBanner subscribes to WS `kind: "alerts"` on mount
- [ ] New alerts appear within 1 second (not 30)
- [ ] Resolved alerts disappear via WS `DELETED` event
- [ ] Initial state loaded via REST on mount
- [ ] Falls back to polling if WS unavailable
- [ ] Unsubscribes on unmount
- [ ] `deno lint && deno fmt --check && deno task build` pass

## Files to Modify

| File | Action |
|------|--------|
| `frontend/islands/AlertBanner.tsx` | Replace polling with WS subscription |
