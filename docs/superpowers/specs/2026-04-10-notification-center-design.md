# Notification Center — Design Spec

## Overview

A centralized notification system that aggregates events from all k8sCenter subsystems, delivers them in-app via a real-time feed, and dispatches to external channels (Slack, email digest, generic webhook) based on admin-configured routing rules.

## Goals

- Unified event feed across all subsystems (alerts, policy, GitOps, diagnostics, scanning, cluster health, audit)
- Real-time in-app delivery via WebSocket (no polling)
- External channel dispatch: Slack webhook, email digest (daily/weekly), generic webhook
- Admin-configured routing rules (source + severity → channel)
- Per-user read state tracking
- RBAC-filtered: users only see notifications for resources they can access
- 90-day retention with automated cleanup (same pattern as audit logs)

## Non-Goals

- Per-user external channel configuration (admin-only for external channels)
- Push notifications (browser/mobile)
- Notification grouping or deduplication (v1 — can add later)

---

## Backend

### Package: `internal/notification/`

### Types

```go
type Severity string

const (
    SeverityInfo     Severity = "info"
    SeverityWarning  Severity = "warning"
    SeverityCritical Severity = "critical"
)

type Source string

const (
    SourceAlert      Source = "alert"
    SourcePolicy     Source = "policy"
    SourceGitOps     Source = "gitops"
    SourceDiagnostic Source = "diagnostic"
    SourceScan       Source = "scan"
    SourceCluster    Source = "cluster"
    SourceAudit      Source = "audit"
)

type Notification struct {
    ID           string    `json:"id"`
    Source       Source    `json:"source"`
    Severity    Severity  `json:"severity"`
    Title       string    `json:"title"`
    Message     string    `json:"message"`
    ResourceKind string   `json:"resourceKind,omitempty"`
    ResourceNS   string   `json:"resourceNamespace,omitempty"`
    ResourceName string   `json:"resourceName,omitempty"`
    ClusterID    string   `json:"clusterId,omitempty"`
    CreatedAt   time.Time `json:"createdAt"`
}

type ChannelType string

const (
    ChannelSlack   ChannelType = "slack"
    ChannelEmail   ChannelType = "email"
    ChannelWebhook ChannelType = "webhook"
)

type Channel struct {
    ID        string      `json:"id"`
    Name      string      `json:"name"`
    Type      ChannelType `json:"type"`
    Config    ChannelConfig `json:"config"`
    CreatedBy string      `json:"createdBy"`
    CreatedAt time.Time   `json:"createdAt"`
}

// ChannelConfig is stored as JSONB. Structure depends on Type.
// Slack:   { "webhookUrl": "https://hooks.slack.com/..." }
// Email:   { "recipients": ["ops@team.com"], "schedule": "daily" }
// Webhook: { "url": "https://...", "headers": {"Authorization": "Bearer ..."} }
type ChannelConfig map[string]any

type Rule struct {
    ID             string   `json:"id"`
    Name           string   `json:"name"`
    SourceFilter   []Source `json:"sourceFilter"`   // empty = all sources
    SeverityFilter []Severity `json:"severityFilter"` // empty = all severities
    ChannelID      string   `json:"channelId"`
    Enabled        bool     `json:"enabled"`
    CreatedBy      string   `json:"createdBy"`
    CreatedAt      time.Time `json:"createdAt"`
}
```

### NotificationService

Central service that all subsystems call to emit notifications.

```go
type NotificationService struct {
    store       NotificationStore
    hub         *websocket.Hub
    dispatchers map[ChannelType]Dispatcher
    rules       []Rule          // cached, refreshed on CRUD
    channels    []Channel       // cached, refreshed on CRUD
    mu          sync.RWMutex
}

// Emit persists the notification, broadcasts via WebSocket, and evaluates routing rules.
func (s *NotificationService) Emit(ctx context.Context, n Notification) error

// Dispatchers
type Dispatcher interface {
    Send(ctx context.Context, channel Channel, notifications []Notification) error
    Test(ctx context.Context, channel Channel) error
}
```

**Emit flow:**
1. Persist notification to PostgreSQL
2. Broadcast to WebSocket hub (`notifications` subscription type)
3. Evaluate all enabled rules — for each matching rule, dispatch to the rule's channel
4. Slack and generic webhook dispatch immediately (async goroutine with 5s timeout)
5. Email digest does not dispatch on Emit — handled by cron goroutine

**Email digest goroutine:**
- Runs on a configurable schedule (daily at 08:00 UTC, or weekly Monday 08:00 UTC)
- Queries notifications created since last digest send
- Filters by the rule's source/severity criteria
- Renders an HTML email summary via Go templates
- Sends via existing SMTP notifier (`internal/alerting/` SMTP code extracted to shared package)

### Dispatchers

**SlackDispatcher:**
- POST to incoming webhook URL
- Payload: Slack Block Kit message with severity color, source badge, title, message, resource link
- 5s timeout, log failures (don't retry in v1)

**EmailDigestDispatcher:**
- Uses extracted SMTP sender (shared with alerting)
- HTML template: grouped by source, sorted by severity, includes resource links
- Subject: "k8sCenter Notification Digest — {date} — {count} notifications"

**WebhookDispatcher:**
- POST JSON payload to configured URL with optional custom headers
- Payload matches the Notification struct (same as API response format)
- 5s timeout, log failures

### Event Producers

Each subsystem emits notifications by calling `notificationService.Emit()`. Minimal coupling — producers don't know about channels or rules.

| Producer | Trigger | Severity | Example Title |
|---|---|---|---|
| Alertmanager handler | Webhook receive (firing) | Maps from alert severity label | "Alert firing: HighMemoryUsage" |
| Alertmanager handler | Webhook receive (resolved) | info | "Alert resolved: HighMemoryUsage" |
| Policy handler | New violation on cache refresh | warning (non-blocking), critical (blocking) | "Policy violation: disallow-latest-tag" |
| GitOps handler | Sync status change (WebSocket watch) | critical (failed), warning (progressing), info (synced) | "Argo CD sync failed: my-app" |
| Diagnostics | Rule failure from periodic/on-demand check | warning (non-fatal), critical (CrashLoop, ImagePull) | "CrashLoopBackOff: nginx in default" |
| Scanning | New critical/high CVE on report refresh | critical (critical CVE), warning (high CVE) | "Critical CVE: CVE-2026-1234 in nginx:latest" |
| ClusterProber | Health state transition | critical (unreachable), info (recovered) | "Cluster unreachable: prod-east" |
| Audit logger | Destructive action (delete, secret reveal) | info | "Secret revealed: db-credentials in prod" |

**Integration pattern:** Each producer adds a single `notificationService.Emit()` call at the point where it already handles the event. No new goroutines, watchers, or polling needed — piggybacks on existing event flows.

---

## PostgreSQL Schema

### Migrations

```sql
-- notifications table
CREATE TABLE notifications (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source        TEXT NOT NULL,
    severity      TEXT NOT NULL,
    title         TEXT NOT NULL,
    message       TEXT NOT NULL DEFAULT '',
    resource_kind TEXT NOT NULL DEFAULT '',
    resource_ns   TEXT NOT NULL DEFAULT '',
    resource_name TEXT NOT NULL DEFAULT '',
    cluster_id    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notifications_created_at ON notifications (created_at DESC);
CREATE INDEX idx_notifications_source ON notifications (source);
CREATE INDEX idx_notifications_severity ON notifications (severity);

-- per-user read tracking
CREATE TABLE notification_reads (
    user_id         TEXT NOT NULL,
    notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, notification_id)
);

-- external channels
CREATE TABLE notification_channels (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    type       TEXT NOT NULL,
    config     JSONB NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- routing rules
CREATE TABLE notification_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    source_filter   TEXT[] NOT NULL DEFAULT '{}',
    severity_filter TEXT[] NOT NULL DEFAULT '{}',
    channel_id      UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### Retention

90-day cleanup via the existing retention goroutine pattern from audit logs:
```sql
DELETE FROM notifications WHERE created_at < now() - INTERVAL '90 days';
```
Runs daily. Cascade deletes `notification_reads` rows.

---

## API Endpoints

All prefixed with `/api/v1/notifications`.

### Notification Feed

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/notifications` | Yes | Paginated feed (query: source, severity, read, since, until, limit, offset) |
| POST | `/notifications/:id/read` | Yes | Mark single notification as read |
| POST | `/notifications/read-all` | Yes | Mark all notifications as read for current user |
| GET | `/notifications/unread-count` | Yes | Unread count for current user (powers badge) |

**RBAC filtering:** The feed query joins against the user's accessible namespaces (same pattern as resource listing). Cluster-scoped notifications (source=cluster) are admin-only.

### Channel Management (Admin)

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/notifications/channels` | Admin | List all channels |
| POST | `/notifications/channels` | Admin | Create channel |
| PUT | `/notifications/channels/:id` | Admin | Update channel |
| DELETE | `/notifications/channels/:id` | Admin | Delete channel (cascades rules) |
| POST | `/notifications/channels/:id/test` | Admin | Send test notification to channel |

**Config masking:** Webhook URLs and Slack tokens masked in GET responses (same pattern as alert settings). Full values only sent on create/update.

### Rule Management (Admin)

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/notifications/rules` | Admin | List all rules |
| POST | `/notifications/rules` | Admin | Create rule |
| PUT | `/notifications/rules/:id` | Admin | Update rule |
| DELETE | `/notifications/rules/:id` | Admin | Delete rule |

---

## WebSocket

New subscription type `notifications` on the existing hub at `/api/v1/ws/resources`.

**Message format:**
```json
{
    "type": "NOTIFICATION",
    "object": {
        "id": "uuid",
        "source": "alert",
        "severity": "critical",
        "title": "Alert firing: HighMemoryUsage",
        "message": "Memory usage above 90% for 5 minutes",
        "resourceKind": "Pod",
        "resourceNamespace": "default",
        "resourceName": "nginx-abc123",
        "createdAt": "2026-04-10T12:00:00Z"
    }
}
```

Hub broadcasts on every `Emit()`. Client-side filters by RBAC (same as resource events — the hub checks namespace access before sending).

---

## Frontend

### Top Bar Bell Icon

- Renders in the top bar (right side, next to theme/user)
- Badge shows unread count (driven by WebSocket `NOTIFICATION` messages + initial `GET /unread-count`)
- Click opens a dropdown panel (not a full page):
  - List of recent notifications (last 20)
  - Each item: severity dot (color), source badge, title, relative timestamp
  - Click item → navigates to resource detail page, marks as read
  - "Mark all read" button
  - "View all" link → `/admin/notifications`

### Notification Page (`/admin/notifications`)

Three sub-tabs via SubNav:

**Feed tab** (`/admin/notifications` or `/admin/notifications/feed`):
- `NotificationFeed` island
- Filterable table: source dropdown, severity dropdown, read/unread toggle, date range
- Paginated, sorted by newest first
- Click-through to resource detail pages
- Bulk "mark as read" action

**Channels tab** (`/admin/notifications/channels`):
- `NotificationChannels` island
- List of configured channels with type icon, name, created date
- Create/edit modal: channel type selector, type-specific config form (Slack: webhook URL; Email: recipients + schedule; Webhook: URL + headers)
- "Test" button per channel — sends a test notification
- Delete with confirmation

**Rules tab** (`/admin/notifications/rules`):
- `NotificationRules` island
- List of routing rules with name, source filter, severity filter, channel name, enabled toggle
- Create/edit modal: name, source multiselect, severity multiselect, channel dropdown, enabled checkbox
- Enable/disable toggle inline (no modal needed)
- Delete with confirmation

### Nav Changes

- Bell icon component in top bar (always visible, all users)
- "Notifications" entry in Admin nav section (admin only, for channel/rule config)
- Command palette: "Notifications" quick action

---

## Security

- **RBAC filtering:** Notification feed filtered by user's namespace access. Cluster-scoped notifications (cluster health) admin-only.
- **Admin gate:** Channel and rule CRUD requires admin role.
- **Encrypted secrets:** Webhook URLs and Slack tokens stored encrypted in JSONB config (AES-256-GCM, same as cluster credentials in `internal/store/encrypt.go`).
- **Config masking:** Sensitive fields masked in API GET responses.
- **Audit logging:** Channel/rule create/update/delete actions audit logged.
- **Rate limiting:** Emit has an internal rate limiter (100 notifications/minute per source) to prevent runaway producers from flooding the system.

---

## Shared Code Extraction

The SMTP sending logic in `internal/alerting/` needs to be extracted to a shared location so both alerting and notification email digest can use it. Proposed: `internal/smtp/` package with a `Sender` interface.

---

## File Inventory

### New Files
- `internal/notification/service.go` — NotificationService, Emit, rule evaluation
- `internal/notification/types.go` — Notification, Channel, Rule, Severity, Source types
- `internal/notification/handler.go` — HTTP handlers for all endpoints
- `internal/notification/store.go` — PostgreSQL CRUD (notifications, reads, channels, rules)
- `internal/notification/dispatcher_slack.go` — Slack webhook dispatcher
- `internal/notification/dispatcher_email.go` — Email digest dispatcher + cron goroutine
- `internal/notification/dispatcher_webhook.go` — Generic webhook dispatcher
- `internal/notification/dispatcher.go` — Dispatcher interface
- `internal/smtp/sender.go` — Extracted SMTP sender (from alerting)
- `backend/internal/store/migrations/000007_create_notifications.up.sql`
- `backend/internal/store/migrations/000007_create_notifications.down.sql`
- `frontend/islands/NotificationBell.tsx` — Top bar bell + dropdown
- `frontend/islands/NotificationFeed.tsx` — Full feed page with filters
- `frontend/islands/NotificationChannels.tsx` — Channel CRUD
- `frontend/islands/NotificationRules.tsx` — Rule CRUD
- `frontend/routes/admin/notifications/index.tsx` — Feed page
- `frontend/routes/admin/notifications/channels.tsx` — Channels page
- `frontend/routes/admin/notifications/rules.tsx` — Rules page
- `frontend/lib/notification-types.ts` — TypeScript interfaces
- `frontend/components/ui/NotificationBadges.tsx` — Severity/source badges

### Modified Files
- `internal/server/routes.go` — register notification endpoints
- `internal/server/server.go` — wire NotificationService, pass to producers
- `internal/alerting/handler.go` — add Emit call on webhook receive
- `internal/policy/handler.go` — add Emit call on violation detection
- `internal/gitops/handler.go` — add Emit call on sync status change
- `internal/diagnostics/handler.go` — add Emit call on rule failures
- `internal/scanning/handler.go` — add Emit call on new CVEs
- `internal/k8s/cluster_prober.go` — add Emit call on health state transition
- `internal/audit/logger.go` — add Emit call on destructive actions
- `internal/alerting/notifier.go` — extract SMTP logic to `internal/smtp/`
- `internal/websocket/hub.go` — add notifications subscription type
- `frontend/routes/_layout.tsx` — add NotificationBell to top bar
- `frontend/routes/admin/` — add notifications sub-routes
- `frontend/lib/api.ts` — add notification API methods
- `frontend/lib/constants.ts` — add nav entries

---

## Roadmap Context

This is Phase 11, item 1 of the new roadmap:

1. **Notification Center** ← this spec
2. Resource Quota & LimitRange Management
3. Backup & Restore (Velero)
4. Service Mesh Observability
5. Cert-Manager Integration
6. External Secrets Operator
7. Saved Views & Custom Dashboards
