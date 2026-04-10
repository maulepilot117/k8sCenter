# Notification Center — Implementation Plan

**Spec:** `docs/superpowers/specs/2026-04-10-notification-center-design.md`
**Phase:** 11 — Feature 1 of 7 on the new roadmap
**Branch:** `feat/notification-center`

> **Naming:** `internal/notification/` exists (Flux Notification Controller).
> New package: `internal/notifications/` (plural). Consistent with full-word convention
> used by all other packages (`alerting`, `diagnostics`, `monitoring`, etc.).

---

## Review Decisions

Applied from DHH, Simplicity, Architecture, and Spec-Flow reviews:

| Decision | Rationale |
|---|---|
| **No Dispatcher interface** | 3 channel types handled via `switch` in service. Extract interface when a 4th type appears. |
| **No Store interface** | One PostgreSQL implementation. Concrete `Store` struct. Tests use real DB. |
| **No SMTP extraction** | Email digest calls `*alerting.Notifier` directly. Extract to `internal/smtp/` when a 3rd consumer appears. |
| **No per-source rate limiter** | Buffered channel (1000) with drop semantics is sufficient. Add rate limiter if runaway producers observed. |
| **Dispatch semaphore** | `semaphore(20)` bounds concurrent goroutines across all async dispatch. Prevents FD exhaustion in storms. |
| **15-min dedup window** | Suppress duplicate `(source, kind, ns, name, title)` within 15 minutes. Prevents feed noise from diagnostic/scanning refresh cycles. |
| **SSRF blocklist on channel URLs** | Reuse `ValidateRemoteURL` from cluster prober at channel create/update time. |
| **Channel error tracking** | `last_error` + `last_error_at` columns on channels table. Updated on dispatch failure. Visible in admin UI. |
| **WebSocket: strip resource fields** | Hub broadcasts `{type, id, source, severity, title}` only. Resource details fetched via REST. Prevents namespace leakage to unauthorized users. |
| **Regular user feed route** | `/notifications` for all users (feed only). `/admin/notifications` for admin (channels + rules tabs). Bell "View all" targets role-appropriate route. |
| **Audit → Emit guard** | `Emit` skips audit-source notifications to prevent `audit → Emit → audit` circular loop. |
| **Badge cap 99+** | Display "99+" when unread count exceeds 99. |
| **`last_sent_at` on channels** | Tracks email digest last send time. Advanced after successful send; skipped (but still advanced) when zero matching notifications. |

---

## Phase 11A: Backend (4 steps)

### Step 1: Schema + Store + Types

**New files:**
- `backend/internal/store/migrations/000007_create_notification_center.up.sql`
- `backend/internal/store/migrations/000007_create_notification_center.down.sql`
- `backend/internal/notifications/store.go` — concrete `Store` struct + all PostgreSQL methods
- `backend/internal/notifications/types.go` — merged: Notification, Channel, ChannelConfig, Rule, Severity, Source, ListOpts

**Migration (up):**
```sql
CREATE TABLE nc_notifications (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source        TEXT NOT NULL,
    severity      TEXT NOT NULL CHECK (severity IN ('critical','warning','info')),
    title         TEXT NOT NULL,
    message       TEXT NOT NULL DEFAULT '',
    resource_kind TEXT NOT NULL DEFAULT '',
    resource_ns   TEXT NOT NULL DEFAULT '',
    resource_name TEXT NOT NULL DEFAULT '',
    cluster_id    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_nc_notif_created_at ON nc_notifications (created_at DESC);
CREATE INDEX idx_nc_notif_source ON nc_notifications (source);
CREATE INDEX idx_nc_notif_dedup ON nc_notifications (source, resource_kind, resource_ns, resource_name, title, created_at DESC);

CREATE TABLE nc_reads (
    user_id         TEXT NOT NULL,
    notification_id UUID NOT NULL REFERENCES nc_notifications(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, notification_id)
);

CREATE INDEX idx_nc_reads_notification_id ON nc_reads (notification_id);

CREATE TABLE nc_channels (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    type          TEXT NOT NULL CHECK (type IN ('slack','email','webhook')),
    config        BYTEA NOT NULL,
    created_by    TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ,
    updated_by    TEXT,
    last_sent_at  TIMESTAMPTZ,
    last_error    TEXT,
    last_error_at TIMESTAMPTZ
);

CREATE TABLE nc_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    source_filter   TEXT[] NOT NULL DEFAULT '{}',
    severity_filter TEXT[] NOT NULL DEFAULT '{}',
    channel_id      UUID NOT NULL REFERENCES nc_channels(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ,
    updated_by      TEXT
);
```

**Store struct** (concrete, no interface):
```go
type Store struct {
    pool   *pgxpool.Pool
    secret string // for encrypt/decrypt
}

// Methods:
// InsertNotification, ListNotifications, UnreadCount, MarkRead, MarkAllRead, PruneOlderThan
// DedupCheck (source, kind, ns, name, title within 15 min)
// ListChannels, GetChannel, CreateChannel, UpdateChannel, DeleteChannel, UpdateChannelError, UpdateChannelLastSent
// ListRules, CreateRule, UpdateRule, DeleteRule
// NotificationsSince (for email digest)
```

**Channel config encryption:** BYTEA with AES-256-GCM (`store.Encrypt`/`store.Decrypt`). JSON marshal → encrypt → store. Read: decrypt → unmarshal.

**SSRF validation:** `CreateChannel` and `UpdateChannel` call `ValidateRemoteURL` on webhook/Slack URLs before persisting.

**RBAC-filtered list query:**
```sql
SELECT n.*, (nr.notification_id IS NOT NULL) AS read
FROM nc_notifications n
LEFT JOIN nc_reads nr ON nr.notification_id = n.id AND nr.user_id = $1
WHERE n.created_at > $2
  AND (n.resource_ns = ANY($3) OR n.resource_ns = '')
ORDER BY n.created_at DESC
LIMIT $4 OFFSET $5
```

Namespace list obtained from `RBACChecker.GetSummary()` (60s cached), passed as `ANY($3::text[])`. Cluster-scoped notifications (empty `resource_ns`) included only if user has cluster-wide access.

**Acceptance criteria:**
- [ ] Migration applies and rolls back cleanly
- [ ] Store methods have unit tests with real PostgreSQL
- [ ] Encrypted channel config round-trips correctly
- [ ] RBAC filtering excludes inaccessible namespaces
- [ ] Dedup check suppresses within 15-min window
- [ ] SSRF validation rejects internal URLs
- [ ] `go vet` passes

---

### Step 2: Service + Dispatch

**New files:**
- `backend/internal/notifications/service.go` — NotificationService, Emit, dispatch loop, channel-type switch

**Service struct:**
```go
type NotificationService struct {
    store     *Store
    hub       *websocket.Hub
    notifier  *alerting.Notifier // for email digest (existing SMTP pipeline)
    queue     chan Notification   // buffered, capacity 1000
    sem       chan struct{}       // dispatch semaphore, capacity 20
    rules     []Rule             // cached, refreshed on CRUD + startup
    channels  []Channel          // cached, refreshed on CRUD + startup
    mu        sync.RWMutex
    logger    *slog.Logger
}
```

**Emit flow:**
1. Dedup check: query store for same `(source, kind, ns, name, title)` within 15 min → skip if exists
2. Persist to PostgreSQL (sync)
3. Broadcast to WebSocket hub — **stripped payload** (id, source, severity, title only, no resource fields)
4. Enqueue for external dispatch (non-blocking send; drop + log if queue full)

**Dispatch goroutine** (`Run(ctx)`):
```go
for n := range s.queue {
    rules := s.matchingRules(n)
    for _, rule := range rules {
        ch := s.channelByID(rule.ChannelID)
        if ch == nil { continue }
        if ch.Type == ChannelEmail { continue } // email is digest-only
        s.sem <- struct{}{} // acquire semaphore
        go func(ch Channel, n Notification) {
            defer func() { <-s.sem }()
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer cancel()
            if err := s.dispatch(ctx, ch, n); err != nil {
                s.logger.Error("dispatch failed", "channel", ch.Name, "error", err)
                s.store.UpdateChannelError(ctx, ch.ID, err.Error())
            }
        }(ch, n)
    }
}
```

**dispatch method** (switch, no interface):
```go
func (s *NotificationService) dispatch(ctx context.Context, ch Channel, n Notification) error {
    switch ch.Type {
    case ChannelSlack:
        return s.sendSlack(ctx, ch, n)
    case ChannelWebhook:
        return s.sendWebhook(ctx, ch, n)
    default:
        return fmt.Errorf("unknown channel type: %s", ch.Type)
    }
}
```

**sendSlack:** POST Block Kit payload to webhook URL. Severity → color mapping.

**sendWebhook:** POST JSON payload with custom headers + `X-Signature-256` HMAC-SHA256 + `User-Agent: k8sCenter-Webhook/1.0`.

**Email digest goroutine** (`RunDigest(ctx)`):
- Wall-clock aligned (compute next fire time, `time.After` until then)
- For each email channel: query `NotificationsSince(channel.LastSentAt)`, filter by matching rules
- Skip send if zero matching notifications (still advance `last_sent_at`)
- Render HTML via `html/template` (embedded, 600px table layout, grouped by severity)
- Send via `s.notifier.QueueEmail(recipients, subject, htmlBody)` — reuses existing alerting SMTP pipeline
- Update `last_sent_at` on channel after successful send
- Email channel config: `{ "recipients": ["ops@team.com"], "schedule": "daily"|"weekly" }`

**Test dispatch:** `TestChannel(ctx, ch)` sends a fixed test message ("This is a test from k8sCenter") per channel type. Email test sends immediately (not digest).

**Audit guard:** `Emit` skips if `n.Source == SourceAudit` to prevent circular `audit → Emit → audit`.

**Acceptance criteria:**
- [ ] Emit deduplicates within 15-min window
- [ ] Emit persists, broadcasts (stripped), and enqueues
- [ ] Queue overflow logs warning, doesn't block
- [ ] Dispatch semaphore bounds concurrent goroutines to 20
- [ ] Slack sends Block Kit payload; webhook sends signed JSON
- [ ] Email digest fires at configured time, sends grouped HTML
- [ ] Empty digest skips send but advances last_sent_at
- [ ] Channel errors recorded in `last_error` / `last_error_at`
- [ ] Test method works for all 3 channel types
- [ ] Audit source guard prevents circular emission
- [ ] `go vet` and `go test ./internal/notifications/... -race` pass

---

### Step 3: HTTP Handlers + Routes + WebSocket

**New files:**
- `backend/internal/notifications/handler.go` — HTTP handlers (14 endpoints)

**Modified files:**
- `backend/internal/server/routes.go` — add `registerNotificationsRoutes(ar)`
- `backend/internal/server/server.go` — add `NotificationsService` to Server + Deps
- `backend/cmd/kubecenter/main.go` — wire NotificationService, pass to deps
- `backend/internal/websocket/events.go` — add `"notifications": true` to `allowedKinds`
- `backend/internal/websocket/hub.go` — add `"notifications": true` to `alwaysAllowKinds`

**Route registration:**
```go
func (s *Server) registerNotificationsRoutes(ar chi.Router) {
    h := s.NotificationsHandler
    // Feed endpoints — all authenticated users
    ar.Route("/notifications", func(r chi.Router) {
        r.Get("/", h.HandleList)
        r.Post("/{id}/read", h.HandleMarkRead)
        r.Post("/read-all", h.HandleMarkAllRead)
        r.Get("/unread-count", h.HandleUnreadCount)
        // Admin-only: channels + rules
        r.Route("/channels", func(cr chi.Router) {
            cr.Use(middleware.RequireAdmin)
            // ... CRUD + test
        })
        r.Route("/rules", func(rr chi.Router) {
            rr.Use(middleware.RequireAdmin)
            // ... CRUD
        })
    })
}
```

**RBAC:** Handler calls `RBACChecker.GetSummary()` per request, extracts namespace list, passes to store.

**Config masking:** GET channel responses mask sensitive config fields.

**Audit logging:** Channel/rule create/update/delete log to audit logger.

**WebSocket broadcast** (in `Emit`):
```go
stripped := map[string]any{"id": n.ID, "source": n.Source, "severity": n.Severity, "title": n.Title}
s.hub.HandleEvent("ADDED", "notifications", "", n.ID, stripped)
```
Empty namespace → broadcast to all subscribers. Resource fields omitted to prevent namespace leakage.

**Acceptance criteria:**
- [ ] All 14 endpoints registered and reachable
- [ ] Feed RBAC-filters by namespace access
- [ ] Config masked in GET responses
- [ ] Admin-only endpoints reject non-admin with 403
- [ ] Channel/rule CRUD audit logged
- [ ] WebSocket broadcast sends stripped payload
- [ ] Existing WS functionality unaffected
- [ ] `go vet` passes

---

### Step 4: Event Producer Integration

**Modified files** (one `Emit` call per file):

| File | Trigger | Source | Severity |
|---|---|---|---|
| `alerting/handler.go` ~line 136 | Webhook receive | `alert` | From alert severity label |
| `policy/handler.go` (CRD watch) | New violations | `policy` | warning/critical |
| `gitops/handler.go` (CRD watch) | Sync status transition | `gitops` | critical/warning/info |
| `diagnostics/handler.go` | Rule failures | `diagnostic` | warning/critical |
| `scanning/handler.go` | New critical/high CVEs | `scan` | critical/warning |
| `k8s/cluster_prober.go` ~line 121 | Health state transition | `cluster` | critical/info |
| `audit/logger.go` | Destructive actions | `audit` | info |

**Pattern:**
```go
if s.notifService != nil {
    s.notifService.Emit(ctx, notifications.Notification{
        Source:       notifications.SourceAlert,
        Severity:     mapAlertSeverity(alert.Labels["severity"]),
        Title:        fmt.Sprintf("Alert %s: %s", alert.Status, alert.Labels["alertname"]),
        Message:      alert.Annotations["description"],
        ResourceKind: alert.Labels["kind"],
        ResourceNS:   alert.Labels["namespace"],
        ResourceName: alert.Labels["name"],
    })
}
```

**GitOps state tracking:** Producer maintains last-known sync status per app (in-memory map by composite ID). Only calls `Emit` on actual state transitions (e.g., `Synced→Failed`), not repeated same-state events.

**`notifService` is optional** — nil check before Emit for graceful degradation.

**Acceptance criteria:**
- [ ] Each producer emits correctly typed notifications
- [ ] GitOps producer only emits on state transitions
- [ ] Nil notifService doesn't crash
- [ ] Existing subsystem behavior unchanged
- [ ] All producers compile and pass existing tests
- [ ] `go vet` and `go test ./... -race` pass

---

## Phase 11B: Frontend (4 steps)

### Step 5: TypeScript Types + API Client

**New files:**
- `frontend/lib/notification-types.ts` — TS interfaces for Notification, Channel, Rule
- `frontend/components/ui/NotificationBadges.tsx` — SeverityDot, SourceBadge (inline, CSS custom properties)

**Modified files:**
- `frontend/lib/api.ts` — add notification API methods (list, unreadCount, markRead, markAllRead, channels CRUD, rules CRUD, test)

**Acceptance criteria:**
- [ ] Types match backend response format
- [ ] API methods call all 14 endpoints
- [ ] Badges render with theme-compliant colors
- [ ] `deno lint` and `deno fmt --check` pass

---

### Step 6: Notification Bell Island

**New files:**
- `frontend/islands/NotificationBell.tsx`

**Modified files:**
- `frontend/islands/TopBarV2.tsx` ~line 173 — replace static bell button with `<NotificationBell />`

**Bell island:**
- Unread count via `GET /unread-count` on mount
- WebSocket subscription to `"notifications"` — increment count on ADDED event
- On WS reconnect: re-fetch unread count from REST to reconcile
- Click opens dropdown (360px, max-height 480px, scrollable)
- Each item: severity dot, source badge, title, relative timestamp
- Click item → navigate to resource, mark as read
- "Mark all read" button, "View all" link (admin → `/admin/notifications`, regular → `/notifications`)
- Badge: red dot with count, capped at "99+"
- Click-outside + Escape to close (reuse TopBarV2 pattern)
- SSR: renders bell with zero badge (no client-data gating)

**Acceptance criteria:**
- [ ] Bell appears for all authenticated users
- [ ] Badge updates in real-time via WebSocket
- [ ] Badge reconciles on WS reconnect
- [ ] Dropdown opens/closes correctly
- [ ] "View all" routes to role-appropriate page
- [ ] Badge caps at "99+"
- [ ] Theme-compliant (CSS custom properties)

---

### Step 7: Feed + Admin Pages

**New files:**
- `frontend/routes/notifications/index.tsx` — regular user feed page
- `frontend/routes/admin/notifications/index.tsx` — admin: redirect to feed
- `frontend/routes/admin/notifications/feed.tsx` — admin feed (same island)
- `frontend/routes/admin/notifications/channels.tsx`
- `frontend/routes/admin/notifications/rules.tsx`
- `frontend/islands/NotificationFeed.tsx`
- `frontend/islands/NotificationChannels.tsx`
- `frontend/islands/NotificationRules.tsx`

**NotificationFeed island:**
- Filters: source dropdown, severity dropdown, read/unread toggle
- Paginated table (25/page, max 200)
- Click row → navigate to resource detail
- `useWsRefetch` for live updates (debounce 2s)
- Empty state: "No notifications yet"

**NotificationChannels island (admin):**
- Table: type icon, name, status indicator (green = OK, red = last_error exists), created date
- Create/edit modal: type selector → type-specific form
- "Test" button per channel
- Delete with type-to-confirm
- Config values masked in display

**NotificationRules island (admin):**
- Table: name, source/severity filter tags, channel name, enabled toggle
- Create/edit modal: name, source multiselect, severity multiselect, channel dropdown
- Inline enable/disable toggle
- Delete with confirmation

**Admin SubNav tabs:** Feed, Channels, Rules

**Acceptance criteria:**
- [ ] Regular user sees feed at `/notifications`
- [ ] Admin sees feed + channels + rules at `/admin/notifications/*`
- [ ] Channel error status visible in admin channels list
- [ ] All CRUD operations work
- [ ] Test button works for all 3 types
- [ ] SubNav tabs navigate correctly
- [ ] `deno lint` and `deno fmt --check` pass

---

### Step 8: Nav + Command Palette + Tests

**Modified files:**
- `frontend/lib/constants.ts` — add Notifications to admin section tabs, add command palette entry

**New files:**
- `backend/internal/notifications/service_test.go`
- `backend/internal/notifications/store_test.go`
- `e2e/notification-center.spec.ts`

**Unit tests:**
- Emit: dedup, persist, broadcast, enqueue
- Dispatch: Slack payload format, webhook HMAC, email digest rendering
- Store: CRUD, RBAC filtering, retention pruning, dedup check
- Rule matching: exact, wildcard, no match

**E2E tests:**
- Bell badge updates on notification
- Feed page filters and pagination
- Admin channel CRUD + test button
- Admin rule CRUD + toggle
- Mark read / mark all read

**Final verification (per CLAUDE.md):**
- [ ] `deno fmt --check` in `frontend/`
- [ ] `deno lint` in `frontend/`
- [ ] `go vet ./...` in `backend/`
- [ ] `go test ./... -race` in `backend/`
- [ ] `make lint` and `make test` pass

---

## File Inventory

### New Files (16)

| File | Purpose |
|---|---|
| `backend/internal/store/migrations/000007_create_notification_center.up.sql` | Schema |
| `backend/internal/store/migrations/000007_create_notification_center.down.sql` | Rollback |
| `backend/internal/notifications/types.go` | Types, constants |
| `backend/internal/notifications/store.go` | PostgreSQL CRUD (concrete struct) |
| `backend/internal/notifications/service.go` | Service, Emit, dispatch, digest, switch-based channel send |
| `backend/internal/notifications/handler.go` | HTTP handlers (14 endpoints) |
| `backend/internal/notifications/service_test.go` | Service + dispatch tests |
| `backend/internal/notifications/store_test.go` | Store + RBAC tests |
| `frontend/lib/notification-types.ts` | TypeScript interfaces |
| `frontend/components/ui/NotificationBadges.tsx` | Severity/source badges |
| `frontend/islands/NotificationBell.tsx` | Top bar bell + dropdown |
| `frontend/islands/NotificationFeed.tsx` | Feed page |
| `frontend/islands/NotificationChannels.tsx` | Channel CRUD (admin) |
| `frontend/islands/NotificationRules.tsx` | Rule CRUD (admin) |
| `frontend/routes/notifications/index.tsx` | Regular user feed page |
| `e2e/notification-center.spec.ts` | E2E tests |

### New Routes (3 admin)

| File | Purpose |
|---|---|
| `frontend/routes/admin/notifications/index.tsx` | Redirect to feed |
| `frontend/routes/admin/notifications/feed.tsx` | Admin feed |
| `frontend/routes/admin/notifications/channels.tsx` | Admin channels |
| `frontend/routes/admin/notifications/rules.tsx` | Admin rules |

### Modified Files (14)

| File | Change |
|---|---|
| `backend/internal/server/routes.go` | Register notifications routes |
| `backend/internal/server/server.go` | Add NotificationsService to Server + Deps |
| `backend/cmd/kubecenter/main.go` | Wire NotificationService |
| `backend/internal/websocket/events.go` | Add "notifications" to allowedKinds |
| `backend/internal/websocket/hub.go` | Add "notifications" to alwaysAllowKinds |
| `backend/internal/alerting/handler.go` | Add Emit call |
| `backend/internal/policy/handler.go` | Add Emit call in watch callback |
| `backend/internal/gitops/handler.go` | Add Emit call + state tracking |
| `backend/internal/diagnostics/handler.go` | Add Emit call |
| `backend/internal/scanning/handler.go` | Add Emit call |
| `backend/internal/k8s/cluster_prober.go` | Add Emit call on state transition |
| `backend/internal/audit/logger.go` | Add Emit call on destructive actions |
| `frontend/islands/TopBarV2.tsx` | Replace bell button with NotificationBell island |
| `frontend/lib/constants.ts` | Add notifications tabs + command palette |
| `frontend/lib/api.ts` | Add notification API methods |

---

## Implementation Order

```
Phase 11A (Backend — 4 steps):
  Step 1: Schema + Store + Types       ← foundation
  Step 2: Service + Dispatch            ← depends on Step 1
  Step 3: Handlers + Routes + WebSocket ← depends on Step 2
  Step 4: Event Producers               ← depends on Step 2 (parallel with 3)

Phase 11B (Frontend — 4 steps):
  Step 5: Types + API Client            ← depends on Step 3 (API exists)
  Step 6: Notification Bell             ← depends on Step 5
  Step 7: Feed + Admin Pages            ← depends on Step 5 (parallel with 6)
  Step 8: Nav + Tests + Verification    ← depends on all above
```

**File count:** 20 new + 15 modified = 35 files total (down from 40)
**Sub-agent swarming:** Steps 1-2 as one agent, Steps 3-4 as another, Steps 5-8 as a third.
