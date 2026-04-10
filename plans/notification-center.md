# Notification Center — Implementation Plan

**Spec:** `docs/superpowers/specs/2026-04-10-notification-center-design.md`
**Phase:** 11 — Feature 1 of 7 on the new roadmap
**Branch:** `feat/notification-center`

> **Naming note:** `internal/notification/` already exists (Flux Notification Controller handlers).
> The new package is `internal/notifcenter/` to avoid collision.

---

## Phase 11A: Backend Core (Types, Store, Service, API)

### Step 1: PostgreSQL Schema + Store

**New files:**
- `backend/internal/store/migrations/000007_create_notification_center.up.sql`
- `backend/internal/store/migrations/000007_create_notification_center.down.sql`
- `backend/internal/notifcenter/store.go`

**Migration (up):**
```sql
CREATE TABLE notif_center_notifications (
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

CREATE INDEX idx_nc_notifications_created_at ON notif_center_notifications (created_at DESC);
CREATE INDEX idx_nc_notifications_source ON notif_center_notifications (source);
CREATE INDEX idx_nc_notifications_severity ON notif_center_notifications (severity);

CREATE TABLE notif_center_reads (
    user_id         TEXT NOT NULL,
    notification_id UUID NOT NULL REFERENCES notif_center_notifications(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, notification_id)
);

CREATE TABLE notif_center_channels (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('slack','email','webhook')),
    config     BYTEA NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE notif_center_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    source_filter   TEXT[] NOT NULL DEFAULT '{}',
    severity_filter TEXT[] NOT NULL DEFAULT '{}',
    channel_id      UUID NOT NULL REFERENCES notif_center_channels(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Store interface** (follows alerting/store.go pattern):
```go
type Store interface {
    // Notifications
    InsertNotification(ctx context.Context, n Notification) (string, error)
    ListNotifications(ctx context.Context, opts ListOpts) ([]Notification, int, error)
    UnreadCount(ctx context.Context, userID string, namespaces []string) (int, error)
    MarkRead(ctx context.Context, userID, notificationID string) error
    MarkAllRead(ctx context.Context, userID string) error
    PruneOlderThan(ctx context.Context, age time.Duration) (int, error)

    // Channels
    ListChannels(ctx context.Context) ([]Channel, error)
    GetChannel(ctx context.Context, id string) (Channel, error)
    CreateChannel(ctx context.Context, ch Channel) (string, error)
    UpdateChannel(ctx context.Context, ch Channel) error
    DeleteChannel(ctx context.Context, id string) error

    // Rules
    ListRules(ctx context.Context) ([]Rule, error)
    CreateRule(ctx context.Context, r Rule) (string, error)
    UpdateRule(ctx context.Context, r Rule) error
    DeleteRule(ctx context.Context, id string) error

    // Digest
    NotificationsSince(ctx context.Context, since time.Time, sourceFilter []string, severityFilter []string) ([]Notification, error)
}
```

**Channel config encryption:** Store `config` as `BYTEA` (not JSONB) so it's AES-256-GCM encrypted at rest using `store.Encrypt()`/`store.Decrypt()` from `internal/store/encrypt.go`. Marshal config JSON → encrypt → store bytes. On read: decrypt → unmarshal.

**RBAC-filtered list query:**
```sql
SELECT n.*, (nr.notification_id IS NOT NULL) AS read
FROM notif_center_notifications n
LEFT JOIN notif_center_reads nr ON nr.notification_id = n.id AND nr.user_id = $1
WHERE n.created_at > $2
  AND (n.resource_ns = ANY($3) OR n.resource_ns = '')
ORDER BY n.created_at DESC
LIMIT $4 OFFSET $5
```

**Retention:** Daily cleanup goroutine (90 days), same pattern as audit log pruning.

**Acceptance criteria:**
- [ ] Migration applies and rolls back cleanly
- [ ] All store methods have unit tests with a real PostgreSQL (testcontainers or dev-db)
- [ ] Encrypted channel config round-trips correctly
- [ ] RBAC filtering excludes notifications from inaccessible namespaces
- [ ] Prune deletes notifications older than 90 days (cascade deletes reads)

---

### Step 2: Types + Service Layer

**New files:**
- `backend/internal/notifcenter/types.go`
- `backend/internal/notifcenter/service.go`

**types.go** — pure data types (Notification, Channel, ChannelConfig, Rule, Severity, Source constants). See spec for full definitions.

**service.go** — `NotificationService`:

```go
type NotificationService struct {
    store       Store
    hub         *websocket.Hub
    dispatchers map[ChannelType]Dispatcher
    queue       chan Notification  // buffered, capacity 1000
    rules       []Rule            // cached
    channels    []Channel         // cached
    mu          sync.RWMutex
    logger      *slog.Logger
}
```

**Emit flow** (called by event producers):
1. Persist to PostgreSQL (sync)
2. Broadcast to WebSocket hub as `"notifications"` kind (non-blocking via `hub.HandleEvent`)
3. Enqueue for external dispatch (non-blocking send to buffered channel; drop + log if full)

**Dispatch goroutine** (`Run(ctx)`, follows alerting/notifier.go pattern):
- Reads from `queue` channel in `for/select` loop
- For each notification, evaluates cached rules (source + severity match)
- For each matching rule, dispatches to the rule's channel via `errgroup.SetLimit(5)` for bounded concurrency
- Each dispatch gets `context.WithTimeout(ctx, 10*time.Second)`

**Rule/channel cache refresh:** Called on channel/rule CRUD operations and on startup. Simple mutex-protected reload from store.

**Rate limiter:** Per-source `golang.org/x/time/rate.Limiter` (100/min per source). If `limiter.Allow()` returns false, skip external dispatch but still persist + broadcast in-app.

**Acceptance criteria:**
- [ ] Emit persists, broadcasts, and enqueues in correct order
- [ ] Queue overflow logs a warning and doesn't block
- [ ] Rate limiter prevents >100 notifications/min per source from hitting external channels
- [ ] Rule evaluation correctly matches source + severity filters (empty filter = match all)
- [ ] Cache refresh is thread-safe

---

### Step 3: Dispatchers (Slack, Webhook, Email Digest)

**New files:**
- `backend/internal/notifcenter/dispatcher.go` — interface
- `backend/internal/notifcenter/dispatcher_slack.go`
- `backend/internal/notifcenter/dispatcher_webhook.go`
- `backend/internal/notifcenter/dispatcher_email.go`
- `backend/internal/smtp/sender.go` — extracted from alerting

**Dispatcher interface:**
```go
type Dispatcher interface {
    Send(ctx context.Context, channel Channel, n Notification) error
    Test(ctx context.Context, channel Channel) error
}
```

**SlackDispatcher:**
- POST to incoming webhook URL
- Block Kit payload: header (title), section (severity + cluster + namespace + resource), section (message)
- Severity → color mapping: critical=`#f7768e`, warning=`#e0af68`, info=`#7aa2f7`
- Per-webhook `rate.NewLimiter(rate.Every(time.Second), 1)` (Slack's 1/sec limit)
- 10s timeout, no retry in v1 (log failures)

**WebhookDispatcher:**
- POST JSON payload matching Notification struct
- Custom headers from channel config
- HMAC-SHA256 signature in `X-Signature-256` header (using channel secret)
- `User-Agent: k8sCenter-Webhook/1.0`
- 10s timeout, log failures

**EmailDigestDispatcher:**
- NOT called per-notification (email is digest-only)
- `RunDigest(ctx)` goroutine: wall-clock aligned to configured hour (daily) or day+hour (weekly)
- On tick: query notifications since last digest → filter by rule → render HTML → send via extracted SMTP sender
- HTML template: table layout (600px), grouped by severity, includes resource links
- Subject: `k8sCenter Notification Digest — {date} — {count} notifications`

**SMTP extraction** (`internal/smtp/sender.go`):
- Extract `send()`, `sendSTARTTLS()`, `sendImplicitTLS()` from `internal/alerting/notifier.go`
- `Sender` struct with `SMTPConfig` + `Send(ctx, to []string, subject, htmlBody string) error`
- `sendWithRetry(ctx, msg, maxAttempts int)` with exponential backoff + jitter
- Update `alerting/notifier.go` to use `smtp.Sender` instead of duplicated code

**Acceptance criteria:**
- [ ] Slack dispatcher sends Block Kit message; logs on failure
- [ ] Webhook dispatcher sends signed JSON payload
- [ ] Email digest goroutine fires at configured time, sends HTML email
- [ ] SMTP extraction doesn't break existing alerting email functionality
- [ ] Test method sends a test notification to each channel type
- [ ] `go vet` and `go test ./internal/notifcenter/... ./internal/smtp/...` pass

---

### Step 4: HTTP Handlers + Route Registration

**New files:**
- `backend/internal/notifcenter/handler.go`

**Modified files:**
- `backend/internal/server/routes.go` — add `registerNotifCenterRoutes(ar)`
- `backend/internal/server/server.go` — add `NotifCenterService *notifcenter.NotificationService` to `Server` and `Deps`
- `backend/cmd/kubecenter/main.go` — wire `NotificationService`, pass to deps

**Handler struct:**
```go
type Handler struct {
    Service     *NotificationService
    AccessChecker *resources.AccessChecker
    AuditLogger audit.Logger
    Logger      *slog.Logger
}
```

**Endpoints** (all under `/api/v1/notifications`):

| Method | Path | Auth | Handler Method |
|---|---|---|---|
| GET | `/notifications` | Yes | `HandleList` |
| POST | `/notifications/:id/read` | Yes | `HandleMarkRead` |
| POST | `/notifications/read-all` | Yes | `HandleMarkAllRead` |
| GET | `/notifications/unread-count` | Yes | `HandleUnreadCount` |
| GET | `/notifications/channels` | Admin | `HandleListChannels` |
| POST | `/notifications/channels` | Admin | `HandleCreateChannel` |
| PUT | `/notifications/channels/:id` | Admin | `HandleUpdateChannel` |
| DELETE | `/notifications/channels/:id` | Admin | `HandleDeleteChannel` |
| POST | `/notifications/channels/:id/test` | Admin | `HandleTestChannel` |
| GET | `/notifications/rules` | Admin | `HandleListRules` |
| POST | `/notifications/rules` | Admin | `HandleCreateRule` |
| PUT | `/notifications/rules/:id` | Admin | `HandleUpdateRule` |
| DELETE | `/notifications/rules/:id` | Admin | `HandleDeleteRule` |

**Route registration** (follows policy/gitops pattern):
```go
func (s *Server) registerNotifCenterRoutes(ar chi.Router) {
    h := s.NotifCenterHandler
    ar.Route("/notifications", func(r chi.Router) {
        r.Get("/", h.HandleList)
        r.Post("/{id}/read", h.HandleMarkRead)
        r.Post("/read-all", h.HandleMarkAllRead)
        r.Get("/unread-count", h.HandleUnreadCount)
        // Admin-only
        r.Route("/channels", func(cr chi.Router) {
            cr.Use(middleware.RequireAdmin)
            cr.Get("/", h.HandleListChannels)
            cr.Post("/", h.HandleCreateChannel)
            cr.Put("/{id}", h.HandleUpdateChannel)
            cr.Delete("/{id}", h.HandleDeleteChannel)
            cr.Post("/{id}/test", h.HandleTestChannel)
        })
        r.Route("/rules", func(rr chi.Router) {
            rr.Use(middleware.RequireAdmin)
            rr.Get("/", h.HandleListRules)
            rr.Post("/", h.HandleCreateRule)
            rr.Put("/{id}", h.HandleUpdateRule)
            rr.Delete("/{id}", h.HandleDeleteRule)
        })
    })
}
```

**Config masking:** GET channel responses mask sensitive config fields (webhookUrl → `****`, slackWebhookUrl → `****`). Same pattern as alerting settings.

**Audit logging:** Channel/rule create/update/delete log to audit logger.

**Acceptance criteria:**
- [ ] All 14 endpoints registered and reachable
- [ ] Feed endpoint RBAC-filters by user's namespace access
- [ ] Channel config masked in GET responses
- [ ] Admin-only endpoints reject non-admin users with 403
- [ ] Channel/rule CRUD operations audit logged
- [ ] `go vet` passes

---

### Step 5: WebSocket Integration

**Modified files:**
- `backend/internal/websocket/events.go` — add `"notifications": true` to `allowedKinds` (~line 57)
- `backend/internal/websocket/hub.go` — add `"notifications": true` to `alwaysAllowKinds` (~line 117)

**Broadcast call** (in `NotificationService.Emit`):
```go
s.hub.HandleEvent("ADDED", "notifications", n.ResourceNS, n.ID, notifJSON)
```

Since `"notifications"` is in `alwaysAllowKinds`, it bypasses the k8s resource RBAC check in the hub. Client-side filtering handles per-user visibility (the bell island only shows what the REST API returns).

**Acceptance criteria:**
- [ ] Frontend can subscribe to `"notifications"` kind via existing WS client
- [ ] Notifications broadcast on Emit reach subscribed clients
- [ ] Existing WS functionality (resource events, alerts, flows) unaffected

---

### Step 6: Event Producer Integration

**Modified files** (one `Emit` call added per file):

| File | Integration Point | Trigger |
|---|---|---|
| `internal/alerting/handler.go` ~line 136 | After WebSocket broadcast in webhook handler loop | Alert firing/resolved |
| `internal/policy/handler.go` | In CRD watch `OnChange` callback (wired in main.go) | New violations detected |
| `internal/gitops/handler.go` | In CRD watch `OnChange` callback (wired in main.go) | Sync status transition |
| `internal/diagnostics/handler.go` | After rule evaluation returns findings | Diagnostic failures |
| `internal/scanning/handler.go` | On cache refresh detecting new critical/high CVEs | New vulnerability |
| `internal/k8s/cluster_prober.go` ~line 121-133 | On status transition (compare old vs new) | Cluster health change |
| `internal/audit/logger.go` | Wrap existing logger, emit on destructive actions | Delete, secret reveal |

**Pattern for each producer:**
```go
if s.notifService != nil {
    s.notifService.Emit(ctx, notifcenter.Notification{
        Source:       notifcenter.SourceAlert,
        Severity:     mapAlertSeverity(alert.Labels["severity"]),
        Title:        fmt.Sprintf("Alert %s: %s", alert.Status, alert.Labels["alertname"]),
        Message:      alert.Annotations["description"],
        ResourceKind: alert.Labels["kind"],
        ResourceNS:   alert.Labels["namespace"],
        ResourceName: alert.Labels["name"],
    })
}
```

**`notifService` is optional** — nil check before Emit so existing subsystems work without the notification center (graceful degradation).

**Acceptance criteria:**
- [ ] Each producer emits correctly typed notifications
- [ ] Nil notifService doesn't crash (graceful skip)
- [ ] Existing subsystem behavior unchanged
- [ ] All producers compile and pass existing tests

---

## Phase 11B: Frontend (Bell, Feed, Channels, Rules)

### Step 7: TypeScript Types + API Client

**New files:**
- `frontend/lib/notification-types.ts`
- `frontend/components/ui/NotificationBadges.tsx`

**Modified files:**
- `frontend/lib/api.ts` — add notification API methods

**notification-types.ts:**
```ts
export type NotifSeverity = "critical" | "warning" | "info";
export type NotifSource = "alert" | "policy" | "gitops" | "diagnostic" | "scan" | "cluster" | "audit";

export interface AppNotification {
    id: string;
    source: NotifSource;
    severity: NotifSeverity;
    title: string;
    message: string;
    resourceKind?: string;
    resourceNamespace?: string;
    resourceName?: string;
    clusterId?: string;
    createdAt: string;
    read?: boolean;
}

export interface NotifChannel { ... }
export interface NotifRule { ... }
```

**API methods:**
```ts
export const notificationApi = {
    list: (params) => apiGet<{data: AppNotification[], metadata: {total: number}}>("/v1/notifications", params),
    unreadCount: () => apiGet<{data: {count: number}}>("/v1/notifications/unread-count"),
    markRead: (id: string) => apiPost("/v1/notifications/" + id + "/read"),
    markAllRead: () => apiPost("/v1/notifications/read-all"),
    // channels + rules CRUD...
};
```

**NotificationBadges.tsx:** `SeverityDot`, `SourceBadge` components following `PolicyBadges.tsx` pattern. CSS custom properties for all colors.

**Acceptance criteria:**
- [ ] Types match backend response format
- [ ] API methods correctly call all 14 endpoints
- [ ] Badges render with theme-compliant colors
- [ ] `deno lint` and `deno fmt --check` pass

---

### Step 8: Notification Bell Island

**New files:**
- `frontend/islands/NotificationBell.tsx`

**Modified files:**
- `frontend/islands/TopBarV2.tsx` ~line 173 — replace static bell button with `<NotificationBell />`
- `frontend/routes/_layout.tsx` — ensure NotificationBell island is in SSR output (Fresh requirement)

**NotificationBell island:**
- `useSignal(0)` for unread count
- `useSignal(false)` for dropdown open state
- `useSignal<AppNotification[]>([])` for recent notifications
- `useRef` + click-outside + Escape key (reuse TopBarV2 dropdown pattern)
- On mount: `GET /notifications/unread-count` for initial badge
- WebSocket subscription: `subscribe("notif-bell", "notifications", "", onEvent)` — increment count on new event
- Dropdown panel: 360px wide, max-height 480px, scrollable
- Each item: severity dot, source badge, title, relative timestamp (`timeAgo` helper)
- Click item → navigate to resource detail, mark as read
- "Mark all read" button, "View all" link to `/admin/notifications`
- Badge: absolute-positioned red dot with count (99+ cap)

**Acceptance criteria:**
- [ ] Bell appears in top bar for all authenticated users
- [ ] Badge shows correct unread count on load
- [ ] Badge updates in real-time via WebSocket (no polling)
- [ ] Dropdown opens on click, closes on click-outside and Escape
- [ ] Clicking a notification navigates to the resource and marks it read
- [ ] "Mark all read" clears the badge
- [ ] Theme-compliant (CSS custom properties only)
- [ ] Fresh island renders in SSR output (not gated on client data)

---

### Step 9: Notification Feed Page

**New files:**
- `frontend/routes/admin/notifications/index.tsx` — redirects to feed
- `frontend/routes/admin/notifications/feed.tsx`
- `frontend/islands/NotificationFeed.tsx`

**Modified files:**
- `frontend/lib/constants.ts` — add "Notifications" tab to admin DOMAIN_SECTIONS

**NotificationFeed island:**
- Filter bar: source dropdown, severity dropdown, read/unread toggle, date range picker
- Paginated table (reuse ResourceTable column/sort pattern)
- Each row: severity dot, source badge, title, resource link, cluster, timestamp, read indicator
- Click row → navigate to resource detail
- Bulk "Mark as read" for visible items
- `useWsRefetch` for live updates (debounce 2s)
- Empty state: "No notifications yet" with setup instructions link

**Acceptance criteria:**
- [ ] Feed loads with pagination (25 per page)
- [ ] Filters work (source, severity, read/unread)
- [ ] Live updates via WebSocket
- [ ] Resource links navigate correctly
- [ ] Empty state renders when no notifications exist
- [ ] `deno lint` and `deno fmt --check` pass

---

### Step 10: Channel + Rule Management Pages

**New files:**
- `frontend/routes/admin/notifications/channels.tsx`
- `frontend/routes/admin/notifications/rules.tsx`
- `frontend/islands/NotificationChannels.tsx`
- `frontend/islands/NotificationRules.tsx`

**NotificationChannels island:**
- Table: type icon (Slack/email/webhook), name, created date, actions
- Create modal: type selector → type-specific form
  - Slack: name, webhook URL
  - Email: name, recipients (comma-separated), schedule (daily/weekly)
  - Webhook: name, URL, custom headers (key/value pairs)
- "Test" button per channel — calls test endpoint, shows success/failure toast
- Delete with type-to-confirm
- Config values masked in display (webhook URLs show `****...last4`)

**NotificationRules island:**
- Table: name, source filter tags, severity filter tags, channel name, enabled toggle, actions
- Create/edit modal: name, source multiselect, severity multiselect, channel dropdown, enabled checkbox
- Inline enable/disable toggle (PUT without modal)
- Delete with confirmation
- Empty state: "No rules configured — notifications will only appear in-app"

**SubNav tabs** for `/admin/notifications`:
- Feed (default)
- Channels
- Rules

**Acceptance criteria:**
- [ ] Channel CRUD works for all 3 types
- [ ] Test button sends test notification and shows result
- [ ] Config values masked in display
- [ ] Rule CRUD with source/severity multiselect
- [ ] Enable/disable toggle works inline
- [ ] Delete with confirmation for both channels and rules
- [ ] SubNav tabs navigate correctly
- [ ] `deno lint` and `deno fmt --check` pass

---

### Step 11: Nav + Command Palette Integration

**Modified files:**
- `frontend/lib/constants.ts` — add Notifications to admin section tabs, add command palette entry
- `frontend/islands/CommandPalette.tsx` — add "Notifications" quick action

**Acceptance criteria:**
- [ ] "Notifications" tab appears in Admin nav section
- [ ] Command palette "Notifications" action navigates to feed page
- [ ] Tab highlighting works correctly on sub-pages

---

## Phase 11C: Verification + Polish

### Step 12: Integration Testing + E2E

**New files:**
- `backend/internal/notifcenter/service_test.go` — unit tests for Emit, dispatch, rule matching
- `backend/internal/notifcenter/store_test.go` — store CRUD + RBAC filtering
- `backend/internal/notifcenter/handler_test.go` — HTTP handler tests
- `e2e/notification-center.spec.ts` — Playwright E2E tests

**Unit tests (backend):**
- Emit persists + broadcasts + enqueues
- Rule evaluation: exact match, wildcard (empty filter), no match
- Rate limiter: allows under limit, blocks over limit
- Store: CRUD for notifications, channels, rules; RBAC filtering; retention pruning
- Dispatchers: Slack payload format, webhook HMAC signature, email template rendering

**E2E tests:**
- Admin creates Slack channel + rule → notification fires → channel CRUD visible
- Bell badge updates on new notification
- Feed page filters and pagination
- Mark read / mark all read
- Rule enable/disable toggle
- Delete channel cascades rules

**Acceptance criteria:**
- [ ] `go test ./internal/notifcenter/... -race` passes
- [ ] `go test ./internal/smtp/... -race` passes
- [ ] E2E tests pass against kind cluster
- [ ] Existing alerting email tests still pass (SMTP extraction didn't break)

---

### Step 13: Forced Verification

Per CLAUDE.md requirements:
- [ ] `npx tsc --noEmit` (or equivalent type check)
- [ ] `deno fmt --check` in `frontend/`
- [ ] `deno lint` in `frontend/`
- [ ] `go vet ./...` in `backend/`
- [ ] `go test ./... -race` in `backend/`
- [ ] `make lint` passes
- [ ] `make test` passes
- [ ] All new files follow existing naming conventions

---

## File Inventory

### New Files (21)

| File | Purpose |
|---|---|
| `backend/internal/store/migrations/000007_create_notification_center.up.sql` | Schema |
| `backend/internal/store/migrations/000007_create_notification_center.down.sql` | Rollback |
| `backend/internal/notifcenter/types.go` | Types, constants |
| `backend/internal/notifcenter/store.go` | PostgreSQL CRUD |
| `backend/internal/notifcenter/service.go` | Service, Emit, dispatch loop |
| `backend/internal/notifcenter/handler.go` | HTTP handlers (14 endpoints) |
| `backend/internal/notifcenter/dispatcher.go` | Dispatcher interface |
| `backend/internal/notifcenter/dispatcher_slack.go` | Slack Block Kit |
| `backend/internal/notifcenter/dispatcher_webhook.go` | Generic webhook + HMAC |
| `backend/internal/notifcenter/dispatcher_email.go` | Email digest + cron |
| `backend/internal/smtp/sender.go` | Extracted SMTP sender |
| `backend/internal/notifcenter/service_test.go` | Service unit tests |
| `backend/internal/notifcenter/store_test.go` | Store unit tests |
| `backend/internal/notifcenter/handler_test.go` | Handler unit tests |
| `frontend/lib/notification-types.ts` | TypeScript interfaces |
| `frontend/components/ui/NotificationBadges.tsx` | Severity/source badges |
| `frontend/islands/NotificationBell.tsx` | Top bar bell + dropdown |
| `frontend/islands/NotificationFeed.tsx` | Full feed page |
| `frontend/islands/NotificationChannels.tsx` | Channel CRUD |
| `frontend/islands/NotificationRules.tsx` | Rule CRUD |
| `frontend/routes/admin/notifications/index.tsx` | Redirect to feed |

### New Routes (3)

| File | Purpose |
|---|---|
| `frontend/routes/admin/notifications/feed.tsx` | Feed page |
| `frontend/routes/admin/notifications/channels.tsx` | Channels page |
| `frontend/routes/admin/notifications/rules.tsx` | Rules page |

### Modified Files (16)

| File | Change |
|---|---|
| `backend/internal/server/routes.go` | Register notification center routes |
| `backend/internal/server/server.go` | Add NotifCenterService to Server + Deps |
| `backend/cmd/kubecenter/main.go` | Wire NotificationService |
| `backend/internal/websocket/events.go` | Add "notifications" to allowedKinds |
| `backend/internal/websocket/hub.go` | Add "notifications" to alwaysAllowKinds |
| `backend/internal/alerting/handler.go` | Add Emit call |
| `backend/internal/alerting/notifier.go` | Use extracted smtp.Sender |
| `backend/internal/policy/handler.go` | Add Emit call in watch callback |
| `backend/internal/gitops/handler.go` | Add Emit call in watch callback |
| `backend/internal/diagnostics/handler.go` | Add Emit call |
| `backend/internal/scanning/handler.go` | Add Emit call |
| `backend/internal/k8s/cluster_prober.go` | Add Emit call on state transition |
| `backend/internal/audit/logger.go` | Add Emit call on destructive actions |
| `frontend/islands/TopBarV2.tsx` | Replace bell button with NotificationBell island |
| `frontend/lib/constants.ts` | Add admin notifications tabs + command palette |
| `frontend/lib/api.ts` | Add notification API methods |

---

## Implementation Order

```
Phase 11A (Backend):
  Step 1: Schema + Store          ← foundation, no dependencies
  Step 2: Types + Service         ← depends on Step 1
  Step 3: Dispatchers + SMTP      ← depends on Step 2
  Step 4: HTTP Handlers + Routes  ← depends on Steps 2-3
  Step 5: WebSocket Integration   ← depends on Step 2
  Step 6: Event Producers         ← depends on Step 2 (can parallel with 4-5)

Phase 11B (Frontend):
  Step 7: Types + API Client      ← depends on Step 4 (API must exist)
  Step 8: Notification Bell       ← depends on Steps 5, 7
  Step 9: Feed Page               ← depends on Step 7 (can parallel with 8)
  Step 10: Channel + Rule Pages   ← depends on Step 7 (can parallel with 8-9)
  Step 11: Nav + Command Palette  ← depends on Steps 9-10

Phase 11C (Verification):
  Step 12: Tests                  ← depends on all above
  Step 13: Lint + Type Check      ← final gate
```

**Estimated file count:** 24 new + 16 modified = 40 files total
**Sub-agent swarming required:** Yes (>5 files). Steps 1-3 can be one agent, Step 4-6 another, Steps 7-11 a third.
