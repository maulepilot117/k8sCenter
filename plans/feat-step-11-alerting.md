# feat(step11): Alerting — Alertmanager Webhook, SMTP Email, Alert Rules CRUD

## Overview

Step 11 adds an alerting subsystem to KubeCenter that receives Prometheus Alertmanager webhooks, broadcasts alerts to WebSocket clients in real-time, sends SMTP email notifications (with rate limiting and digest aggregation), manages PrometheusRule CRDs via the UI, and provides an alert history store. This completes Phase C (Advanced Features) of the MVP.

## Problem Statement

KubeCenter users monitoring their clusters via the Step 9 Prometheus/Grafana integration currently have no way to receive proactive notifications when alerts fire. They must actively check dashboards. Step 11 closes this gap by:
- Receiving Alertmanager webhooks and displaying active alerts in real-time
- Sending email notifications for critical events
- Providing a UI for creating and managing Prometheus alerting rules (PrometheusRule CRDs)
- Maintaining an alert history for post-incident review

## Dependencies

- **Step 9 (Monitoring)** — Prometheus/Grafana auto-discovery, monitoring handler patterns, Prometheus Operator CRD detection
- **Step 10 (CSI/CNI)** — Dynamic client with impersonation (`DynamicClientForUser`), CRD availability checking pattern (`sync.Once` + DiscoveryClient)
- **Step 5 (WebSocket)** — Hub pattern for real-time broadcasting

---

## Specification Decisions

These decisions resolve the gaps identified during spec-flow analysis. Each is documented here as the authoritative answer.

### D1. RBAC Gate for "alerts" WebSocket Subscription

**Decision:** Skip the per-resource RBAC check for the "alerts" kind. JWT authentication alone is sufficient. Add "alerts" to an `alwaysAllowKinds` set in the hub that bypasses the `SelfSubjectAccessReview` check.

**Rationale:** Alerts are not a Kubernetes resource — there is no `alerts` resource in any API group. A SelfSubjectAccessReview for `list/alerts` against the core group will always be denied. Any authenticated user should see alerts (alerts are operational, not secret). Namespace-level filtering is still applied: if an alert has a `namespace` label, it is only broadcast to clients subscribed to that namespace or to clients with a cluster-wide subscription.

### D2. SMTP Password Persistence

**Decision:** In-memory only. The `PUT /api/v1/alerts/settings` endpoint updates the running process's config. On pod restart, the SMTP password must be re-provided via env var (`KUBECENTER_ALERTING_SMTPPASSWORD`) or re-entered in the UI.

**Rationale:** Writing secrets to k8s Secrets from the backend would require additional service account permissions and introduces a new write path to audit. In-memory is consistent with how the user store works (in-memory until Step 14 SQLite). The settings page will display a warning: "Settings configured via the UI are stored in memory and will be lost on restart. Use environment variables for persistent configuration."

### D3. Webhook Rate Limiting

**Decision:** Dedicated rate limiter at 300 req/min per IP (5/second). Separate from the auth rate limiter (5/min) and the YAML rate limiter (30/min). This handles legitimate Alertmanager bursts during cascading failures.

**Rationale:** Alertmanager can send many webhook calls in rapid succession during alert storms. The auth rate limiter's 5/min would cause alert loss. 300/min is generous enough for worst-case but still protects against abuse.

### D4. Per-Alert Email Cooldown

**Decision:** 15-minute cooldown per alert fingerprint for "firing" emails. "Resolved" emails always bypass the cooldown (users need to know immediately when an incident ends). Cooldown applies to individual emails only — digests have their own window.

### D5. Rate-Limited Email Behavior

**Decision:** Drop with a `slog.Warn` log entry when the 120/hour global rate limit is reached. Do not queue for deferred delivery (unbounded memory risk). The settings page documents the limit.

### D6. Alerting `enabled` Flag Behavior

**Decision:** Routes are always registered. When `enabled=false`: webhook returns 503 ("alerting is disabled"), email sending is skipped, settings GET returns current config with `enabled: false`. Alert rules CRUD is always available regardless of the flag (it is pure k8s CRD management via impersonation).

### D7. AlertBanner Placement

**Decision:** Dismissible banner in `_layout.tsx`, rendered below the TopBar and above the main content area. Shows count of active firing alerts with severity breakdown (critical/warning). Clicking the banner navigates to the alerts page. Resolved alerts show briefly (3 seconds) as a green "resolved" indicator before hiding.

### D8. Alert Rules UI Mode

**Decision:** YAML-only editor (Monaco) for PrometheusRule CRDs. PromQL expressions and rule group structure are too complex for a form wizard, and the target audience (cluster operators) is comfortable with YAML. The existing Step 7 YAML apply infrastructure handles validation and diff.

### D9. Alert Rules Namespace

**Decision:** `POST /api/v1/alerts/rules` accepts namespace in the request body. The backend creates the PrometheusRule in the specified namespace with `managed-by: kubecenter` label. Same pattern as `POST /api/v1/resources/:kind/:namespace`.

### D10. Digest Email Semantics

**Decision:** Rolling 1-minute window. When the 6th alert fires within 60 seconds of the 1st, all queued individual emails for that window are cancelled and replaced with a single digest. The window resets after the digest sends. Subsequent alerts outside the window send individually (subject to per-alert cooldown). Resolved alerts are never part of a digest — they always send individually.

### D11. Alert Store Size Cap

**Decision:** Maximum 10,000 entries in-memory. When the cap is reached, oldest entries are evicted (FIFO). Prune goroutine runs hourly, removing entries older than 30 days (configurable via `KUBECENTER_ALERTING_RETENTIONDAYS`).

### D12. Pagination Format

**Decision:** Use `limit` + `continue` cursor-based pagination, consistent with all other paginated endpoints in the API. The cursor encodes `receivedAt` timestamp.

### D13. Webhook Authentication

**Decision:** Bearer token via `Authorization: Bearer <token>` header. Alertmanager supports this natively via `http_config.authorization`. The token is set via `KUBECENTER_ALERTING_WEBHOOKTOKEN` and verified with `crypto/subtle.ConstantTimeCompare`. This is simpler than HMAC (which Alertmanager does not natively support) and more secure than no auth.

### D14. Frontend Route Structure

**Decision:** Routes under `/alerting/` (separate nav section from Monitoring):
- `/alerting` — Active alerts + history tabs
- `/alerting/rules` — PrometheusRule CRUD (Monaco YAML editor)
- `/alerting/settings` — SMTP configuration, webhook token display

### D15. WS Endpoint for Alerts

**Decision:** Use the existing `v1/ws/resources` endpoint with kind `"alerts"` added to `allowedKinds`. Do NOT create a separate `/api/v1/ws/alerts` endpoint. The `v1/ws/alerts` entry in the WS proxy allowlist is removed as unnecessary. This keeps the architecture simple — one WS connection per client, multiple subscription kinds.

---

## Technical Approach

### Architecture

```
                                    ┌─────────────────────┐
                                    │    Alertmanager      │
                                    │  (in-cluster)        │
                                    └────────┬────────────┘
                                             │ POST /api/v1/alerts/webhook
                                             │ Authorization: Bearer <token>
                                             ▼
┌──────────────┐    REST/WS     ┌────────────────────────────┐
│   Frontend   │◄──────────────►│       Go Backend           │
│  Deno/Fresh  │                │                            │
│              │                │  ┌──────────────────────┐  │
│  AlertBanner │◄──── WS ──────│──│   WebSocket Hub      │  │
│  AlertsPage  │                │  │   (kind: "alerts")   │  │
│  RulesEditor │                │  └──────────────────────┘  │
│  Settings    │                │  ┌──────────────────────┐  │
│              │                │  │   AlertHandler        │  │
│              │                │  │   - webhook receiver  │  │
│              │                │  │   - alert store       │  │
│              │                │  │   - email notifier    │  │
│              │                │  │   - rules manager     │  │
│              │                │  └──────────┬───────────┘  │
│              │                │             │              │
│              │                │  ┌──────────▼───────────┐  │
│              │                │  │   SMTP Queue         │  │
│              │                │  │   (goroutine)        │  │
│              │                │  └──────────────────────┘  │
│              │                │                            │
│              │                │  ┌──────────────────────┐  │
│              │                │  │   K8s API (imperson.) │  │
│              │                │  │   PrometheusRule CRD  │  │
│              │                │  └──────────────────────┘  │
└──────────────┘                └────────────────────────────┘
```

### Data Flow

1. Alertmanager sends webhook → backend verifies bearer token → parses payload
2. Backend deduplicates by fingerprint → stores in AlertStore → broadcasts to WS hub
3. WS hub fans out to subscribed clients (kind: "alerts", namespace-filtered)
4. Email notifier checks rate limits → queues email → sends with retry (3 attempts)
5. Frontend AlertBanner receives WS event → updates active alert count
6. PrometheusRule CRUD → impersonated dynamic client → k8s API server

---

## Implementation Plan

### Phase 1: Backend Core (alerting package + config + wiring)

#### 1.1 Config

**File: `backend/internal/config/config.go`**
- Add `Alerting AlertingConfig` field to `Config` struct
- Add `AlertingConfig` struct with koanf tags:
  ```go
  type AlertingConfig struct {
      Enabled       bool       `koanf:"enabled"`
      WebhookToken  string     `koanf:"webhooktoken"`
      RetentionDays int        `koanf:"retentiondays"`
      RateLimit     int        `koanf:"ratelimit"`      // max emails/hour
      SMTP          SMTPConfig `koanf:"smtp"`
  }

  type SMTPConfig struct {
      Host        string `koanf:"host"`
      Port        int    `koanf:"port"`
      Username    string `koanf:"username"`
      Password    string `koanf:"password"`
      From        string `koanf:"from"`
      TLSInsecure bool   `koanf:"tlsinsecure"` // dev only
  }
  ```

**File: `backend/internal/config/defaults.go`**
- Add defaults: `RetentionDays=30`, `RateLimit=120`, `SMTP.Port=587`, `Enabled=false`

**Env var mapping:**
- `KUBECENTER_ALERTING_ENABLED` → `Config.Alerting.Enabled`
- `KUBECENTER_ALERTING_WEBHOOKTOKEN` → `Config.Alerting.WebhookToken`
- `KUBECENTER_ALERTING_RETENTIONDAYS` → `Config.Alerting.RetentionDays`
- `KUBECENTER_ALERTING_RATELIMIT` → `Config.Alerting.RateLimit`
- `KUBECENTER_ALERTING_SMTP_HOST` → `Config.Alerting.SMTP.Host`
- `KUBECENTER_ALERTING_SMTP_PORT` → `Config.Alerting.SMTP.Port`
- `KUBECENTER_ALERTING_SMTP_USERNAME` → `Config.Alerting.SMTP.Username`
- `KUBECENTER_ALERTING_SMTP_PASSWORD` → `Config.Alerting.SMTP.Password`
- `KUBECENTER_ALERTING_SMTP_FROM` → `Config.Alerting.SMTP.From`
- `KUBECENTER_ALERTING_SMTP_TLSINSECURE` → `Config.Alerting.SMTP.TLSInsecure`

**Gotcha:** koanf's env provider uses `_` → `.` mapping. `KUBECENTER_ALERTING_SMTP_HOST` maps to `alerting.smtp.host` which maps to `Config.Alerting.SMTP.Host`. This works because koanf's underscore-to-dot conversion handles nested structs. Verify with a test.

#### 1.2 Webhook Receiver

**File: `backend/internal/alerting/webhook.go`**

Types — define our own types (don't import the full alertmanager dependency for just the webhook payload):
```go
type WebhookPayload struct {
    Version           string            `json:"version"`
    GroupKey          string            `json:"groupKey"`
    TruncatedAlerts   int               `json:"truncatedAlerts"`
    Status            string            `json:"status"`
    Receiver          string            `json:"receiver"`
    GroupLabels       map[string]string `json:"groupLabels"`
    CommonLabels      map[string]string `json:"commonLabels"`
    CommonAnnotations map[string]string `json:"commonAnnotations"`
    ExternalURL       string            `json:"externalURL"`
    Alerts            []WebhookAlert    `json:"alerts"`
}

type WebhookAlert struct {
    Status       string            `json:"status"`
    Labels       map[string]string `json:"labels"`
    Annotations  map[string]string `json:"annotations"`
    StartsAt     time.Time         `json:"startsAt"`
    EndsAt       time.Time         `json:"endsAt"`
    GeneratorURL string            `json:"generatorURL"`
    Fingerprint  string            `json:"fingerprint"`
}
```

Processing logic:
- Read body with `io.LimitReader` (1MB max)
- Parse JSON into `WebhookPayload`
- Validate: `version` must be "4", `alerts` must not be empty, each alert must have `fingerprint` and `labels.alertname`
- For each alert:
  - If status="firing" and fingerprint not in active store → new alert (store + broadcast + queue email)
  - If status="firing" and fingerprint already active → update `updatedAt`, no re-broadcast
  - If status="resolved" and fingerprint in active store → mark resolved, broadcast MODIFIED event, queue resolved email
  - If status="resolved" and fingerprint not found → ignore (already resolved)
- Return 200 with `{"data":{"accepted": N}}` where N is the count of processed alerts

#### 1.3 Alert Store

**File: `backend/internal/alerting/store.go`**

Interface:
```go
type Store interface {
    // Record stores or updates an alert event.
    Record(ctx context.Context, event AlertEvent) error
    // ActiveAlerts returns currently firing alerts.
    ActiveAlerts(ctx context.Context) ([]AlertEvent, error)
    // List returns paginated alert history.
    List(ctx context.Context, opts ListOptions) ([]AlertEvent, string, error) // items, continueToken, error
    // Resolve marks an alert as resolved.
    Resolve(ctx context.Context, fingerprint string, endsAt time.Time) error
    // Prune removes events older than the given time. Returns count removed.
    Prune(ctx context.Context, olderThan time.Time) (int, error)
}
```

In-memory implementation:
- `sync.RWMutex`-protected maps
- `active map[string]*AlertEvent` keyed by fingerprint (current firing alerts)
- `history []AlertEvent` sorted by `receivedAt` descending (capped at 10,000 entries)
- `Prune` runs hourly via a goroutine started from `main.go`
- When history reaches 10,000 entries, oldest entries are evicted on insert

`AlertEvent` struct:
```go
type AlertEvent struct {
    ID          string            `json:"id"`
    ClusterID   string            `json:"clusterID"`
    Fingerprint string            `json:"fingerprint"`
    Status      string            `json:"status"`       // "firing" or "resolved"
    AlertName   string            `json:"alertName"`
    Namespace   string            `json:"namespace"`
    Severity    string            `json:"severity"`
    Labels      map[string]string `json:"labels"`
    Annotations map[string]string `json:"annotations"`
    StartsAt    time.Time         `json:"startsAt"`
    EndsAt      time.Time         `json:"endsAt,omitempty"`
    ReceivedAt  time.Time         `json:"receivedAt"`
    ResolvedAt  time.Time         `json:"resolvedAt,omitempty"`
}
```

#### 1.4 Email Notifier

**File: `backend/internal/alerting/notifier.go`**

Use Go stdlib `net/smtp` with STARTTLS (no external dependency needed for KubeCenter's moderate email volume):
- Dedicated sender goroutine processing a buffered channel (`chan *EmailMessage`, buffer 100)
- 3 retry attempts with exponential backoff (1s, 2s, 4s) + jitter
- Port 587: STARTTLS negotiation. Port 465: implicit TLS via `tls.Dial` + `smtp.NewClient`
- `smtp.PlainAuth` for authentication (covers most providers)
- Rate limiting: `rateLimiter` struct tracking sends per hour (global) and per fingerprint (15-min cooldown)
- Digest logic: track firing alerts in a 1-minute rolling window; when count reaches 6, cancel individual emails and send digest

**File: `backend/internal/alerting/templates/`**
- `alert_firing.html` — Fired alert: name, severity, namespace, description, link to Prometheus, link to KubeCenter
- `alert_resolved.html` — Resolved alert: name, resolution time, duration
- `alert_digest.html` — Digest: count of alerts, severity breakdown, table of alert names, link to KubeCenter alerts page
- `alert_test.html` — Test email: confirms SMTP is working

Templates embedded via `go:embed` and parsed with `html/template` at init.

#### 1.5 Alert Rules Manager

**File: `backend/internal/alerting/rules.go`**

- Uses `DynamicClientForUser` from `k8s.ClientFactory` (same pattern as VolumeSnapshots in Step 10)
- GVR: `monitoring.coreos.com/v1/prometheusrules`
- CRD availability checked via `sync.Once` + `DiscoveryClient`
- List: filtered by `app.kubernetes.io/managed-by=kubecenter` label selector
- Create: inject `managed-by: kubecenter` label, validate name against k8s name regex
- Update: server-side apply with `fieldManager: "kubecenter"`
- Delete: verify `managed-by: kubecenter` label before deleting (refuse to delete unmanaged rules)
- All operations use impersonated dynamic client → k8s RBAC enforced server-side

#### 1.6 Handler

**File: `backend/internal/alerting/handler.go`**

Handler struct:
```go
type Handler struct {
    Store        Store
    Notifier     *Notifier
    Rules        *RulesManager
    Hub          *websocket.Hub
    AuditLogger  audit.Logger
    Logger       *slog.Logger
    ClusterID    string
    WebhookToken string
    Config       *config.AlertingConfig  // mutable for settings updates
    ConfigMu     sync.RWMutex            // protects Config reads/writes
}
```

Endpoints:
- `POST /api/v1/alerts/webhook` — public (bearer token auth, no JWT), webhook rate limiter
- `GET /api/v1/alerts` — authenticated, returns active alerts from store
- `GET /api/v1/alerts/history` — authenticated, paginated history from store
- `GET /api/v1/alerts/rules` — authenticated, lists PrometheusRule CRDs (impersonated)
- `POST /api/v1/alerts/rules` — authenticated, creates PrometheusRule (impersonated), audit logged
- `PUT /api/v1/alerts/rules/{namespace}/{name}` — authenticated, updates PrometheusRule, audit logged
- `DELETE /api/v1/alerts/rules/{namespace}/{name}` — authenticated, deletes PrometheusRule, audit logged
- `GET /api/v1/alerts/settings` — authenticated, returns alerting config (SMTP password masked)
- `PUT /api/v1/alerts/settings` — authenticated, updates in-memory alerting config, audit logged
- `POST /api/v1/alerts/test` — authenticated, sends test email, audit logged, rate limited (1/min per user)

#### 1.7 Server Wiring

**File: `backend/internal/server/server.go`**
- Add `AlertingHandler *alerting.Handler` to `Deps` struct and `Server` struct

**File: `backend/internal/server/routes.go`**
- Add `registerAlertingRoutes(ar chi.Router)` for authenticated endpoints (inside auth group)
- Add webhook route in the public group (alongside `/auth/login`), with its own bearer token middleware and dedicated rate limiter
- Webhook route: `r.With(webhookRateLimiter, webhookAuth).Post("/api/v1/alerts/webhook", s.AlertingHandler.HandleWebhook)`

**File: `backend/cmd/kubecenter/main.go`**
- Create `alerting.Store` (in-memory)
- Create `alerting.Notifier` (if SMTP config provided)
- Create `alerting.RulesManager` (with client factory)
- Create `alerting.Handler` and wire into `Deps`
- Start notifier goroutine: `go notifier.Run(ctx)`
- Start prune goroutine: `go store.RunPruner(ctx, retentionDays)`

#### 1.8 WebSocket Integration

**File: `backend/internal/websocket/events.go`**
- Add `"alerts"` to `allowedKinds` map

**File: `backend/internal/websocket/hub.go`**
- Add `alwaysAllowKinds = map[string]bool{"alerts": true}` — skip RBAC check for these kinds
- In `broadcastEvent`, if kind is in `alwaysAllowKinds`, skip the `CanAccess` check (JWT auth is sufficient)
- Still filter by namespace: if the alert has a namespace label, only send to clients subscribed to that namespace or to clients with a `""` (all namespaces) subscription

#### 1.9 RBAC / Access Extensions

**File: `backend/internal/k8s/resources/access.go`**
- Add `"prometheusrules": "monitoring.coreos.com"` to `apiGroupForResource` map
- This ensures SelfSubjectAccessReview for PrometheusRule CRUD uses the correct API group

#### 1.10 Audit Actions

**File: `backend/internal/audit/logger.go`**
- Add new action constants:
  ```go
  const (
      ActionAlertRuleCreate    Action = "alert_rule_create"
      ActionAlertRuleUpdate    Action = "alert_rule_update"
      ActionAlertRuleDelete    Action = "alert_rule_delete"
      ActionAlertSettingsUpdate Action = "alert_settings_update"
      ActionAlertTest          Action = "alert_test"
  )
  ```

### Phase 2: Frontend

#### 2.1 Nav Section

**File: `frontend/lib/constants.ts`**
- Add "Alerting" section to `NAV_SECTIONS` after "Monitoring":
  ```typescript
  {
    title: "Alerting",
    items: [
      { label: "Active Alerts", href: "/alerting", icon: "alerts" },
      { label: "Alert Rules", href: "/alerting/rules", icon: "rules" },
      { label: "Settings", href: "/alerting/settings", icon: "settings" },
    ],
  },
  ```

**File: `frontend/components/k8s/ResourceIcon.tsx`**
- Add SVG icons for `alerts`, `rules`, `settings` resource types

#### 2.2 AlertBanner Island

**File: `frontend/islands/AlertBanner.tsx`**
- Subscribes to `kind: "alerts"` via existing `ws.ts` client
- Fetches initial state from `GET /api/v1/alerts` on mount
- Displays: count of active firing alerts, severity breakdown (critical badge red, warning badge amber)
- Clicking navigates to `/alerting`
- Resolved alerts show green "resolved" indicator for 3 seconds then fade
- Dismissible (per-session, resets on page refresh)

**File: `frontend/routes/_layout.tsx`**
- Import and render `AlertBanner` island between TopBar and main content area
- Only render when user is authenticated (check auth state)

#### 2.3 Alerts Page

**File: `frontend/routes/alerting/index.tsx`**
- SSR shell rendering `AlertsPage` island

**File: `frontend/islands/AlertsPage.tsx`**
- Two tabs: "Active" and "History"
- Active tab: real-time list of firing alerts from WS + `GET /api/v1/alerts`
  - Columns: Alert Name, Severity, Namespace, Started, Description
  - Severity color coding: critical=red, warning=amber, info=blue
  - Click row → expands to show full labels, annotations, GeneratorURL link
- History tab: paginated list from `GET /api/v1/alerts/history`
  - Columns: Alert Name, Severity, Namespace, Status (firing/resolved), Started, Resolved, Duration
  - Filters: severity dropdown, namespace selector, date range
  - Uses `limit` + `continue` pagination (consistent with ResourceTable)

#### 2.4 Alert Rules Page

**File: `frontend/routes/alerting/rules.tsx`**
- SSR shell rendering `AlertRulesPage` island

**File: `frontend/islands/AlertRulesPage.tsx`**
- Lists PrometheusRule CRDs from `GET /api/v1/alerts/rules`
- Columns: Name, Namespace, Rules Count, Created, Actions (edit/delete)
- "Create Rule" button → opens Monaco YAML editor with a PrometheusRule template
- Edit → loads existing rule YAML into Monaco editor
- Delete → confirmation dialog → `DELETE /api/v1/alerts/rules/:ns/:name`
- If PrometheusRule CRD is not available → show info state: "Prometheus Operator is required for alert rules management"

#### 2.5 Alert Settings Page

**File: `frontend/routes/alerting/settings.tsx`**
- SSR shell rendering `AlertSettings` island

**File: `frontend/islands/AlertSettings.tsx`**
- Form for SMTP configuration:
  - Host, Port, Username, Password (masked input), From address, TLS Skip Verify toggle
  - "Save" → `PUT /api/v1/alerts/settings`
  - "Send Test Email" → `POST /api/v1/alerts/test`
- Webhook configuration section (read-only):
  - Displays the webhook URL: `http://<backend-service>:8080/api/v1/alerts/webhook`
  - Displays Alertmanager receiver YAML snippet for copy-paste configuration
  - Webhook token: masked display with reveal button
- Warning banner: "Settings configured here are stored in memory and lost on pod restart. Use environment variables for persistent configuration."
- Rate limit display: current setting (emails/hour)

---

## Files to Create

```
backend/internal/alerting/
├── handler.go              # HTTP handlers (webhook, alerts CRUD, settings)
├── webhook.go              # Webhook payload types, processing, deduplication
├── store.go                # AlertStore interface + in-memory implementation
├── notifier.go             # Email sender goroutine, rate limiting, digest logic
├── rules.go                # PrometheusRule CRD CRUD via dynamic client
├── alerting_test.go        # Unit tests
└── templates/
    ├── embed.go            # go:embed directive
    ├── alert_firing.html   # Firing alert email template
    ├── alert_resolved.html # Resolved alert email template
    ├── alert_digest.html   # Digest email template
    └── alert_test.html     # Test email template

frontend/routes/alerting/
├── index.tsx               # Active alerts + history page
├── rules.tsx               # PrometheusRule CRUD page
└── settings.tsx            # SMTP settings page

frontend/islands/
├── AlertBanner.tsx         # Global alert banner (in layout)
├── AlertsPage.tsx          # Active/history tabs
├── AlertRulesPage.tsx      # Rules CRUD with Monaco editor
└── AlertSettings.tsx       # SMTP config form
```

## Files to Modify

```
backend/internal/config/config.go       # Add AlertingConfig struct
backend/internal/config/defaults.go     # Add alerting defaults
backend/internal/server/server.go       # Add AlertingHandler to Deps/Server
backend/internal/server/routes.go       # Add alert routes (public webhook + authenticated CRUD)
backend/internal/websocket/events.go    # Add "alerts" to allowedKinds
backend/internal/websocket/hub.go       # Add alwaysAllowKinds bypass for RBAC
backend/internal/k8s/resources/access.go # Add monitoring.coreos.com API group
backend/internal/audit/logger.go        # Add alert audit action constants
backend/cmd/kubecenter/main.go          # Wire alerting handler, start goroutines
frontend/lib/constants.ts               # Add Alerting nav section
frontend/components/k8s/ResourceIcon.tsx # Add alert icons
frontend/routes/_layout.tsx             # Add AlertBanner island
frontend/routes/ws/[...path].ts         # Remove v1/ws/alerts from allowlist (using v1/ws/resources instead)
```

## Dependencies to Add

```
# None — using Go stdlib net/smtp and existing k8s client-go dynamic client
# No new Go dependencies required
# Alertmanager webhook types are defined locally (simple structs)
# PrometheusRule CRD accessed via unstructured dynamic client (no typed client dependency)
```

---

## Acceptance Criteria

### Webhook Receiver
- [ ] `POST /api/v1/alerts/webhook` accepts Alertmanager v4 webhook payloads
- [ ] Bearer token authentication rejects invalid/missing tokens with 401
- [ ] Deduplication by fingerprint: duplicate firing alerts are silently ignored
- [ ] Status transitions (firing→resolved) are correctly tracked
- [ ] Malformed payloads return 400 with descriptive error
- [ ] 1MB body size limit enforced
- [ ] Dedicated rate limiter (300/min) separate from auth endpoints
- [ ] Returns 503 when `alerting.enabled=false`

### Alert Store
- [ ] Active alerts queryable via `GET /api/v1/alerts`
- [ ] Alert history paginated via `GET /api/v1/alerts/history` with `limit` + `continue`
- [ ] History filterable by namespace, severity, status, date range
- [ ] 10,000 entry cap with FIFO eviction
- [ ] Prune goroutine removes entries older than retention period (default 30 days)
- [ ] Thread-safe (concurrent webhook writes + API reads)

### Email Notifications
- [ ] Firing alerts send HTML email via SMTP with STARTTLS
- [ ] Resolved alerts send "resolved" email (bypasses per-alert cooldown)
- [ ] 3 retry attempts with exponential backoff on SMTP failure
- [ ] Global rate limit: 120 emails/hour (configurable), excess dropped with warning log
- [ ] Per-alert cooldown: 15 minutes for firing emails
- [ ] Digest email sent when >5 alerts fire within 1 minute (replaces individual emails)
- [ ] SMTP not configured → emails silently skipped (no errors)
- [ ] `POST /api/v1/alerts/test` sends test email, rate limited 1/min per user

### Alert Rules CRUD
- [ ] `GET /api/v1/alerts/rules` lists PrometheusRule CRDs with `managed-by: kubecenter` label
- [ ] `POST /api/v1/alerts/rules` creates PrometheusRule via impersonated dynamic client
- [ ] `PUT /api/v1/alerts/rules/:ns/:name` updates via server-side apply
- [ ] `DELETE /api/v1/alerts/rules/:ns/:name` refuses to delete rules without `managed-by: kubecenter` label
- [ ] All CRUD operations audit logged
- [ ] PrometheusRule CRD not installed → rules endpoints return empty list (not error)
- [ ] RBAC enforced via impersonation (403 if user lacks PrometheusRule permissions)

### WebSocket
- [ ] "alerts" kind in `allowedKinds` — clients can subscribe via existing WS endpoint
- [ ] RBAC bypassed for "alerts" kind (JWT auth sufficient)
- [ ] Namespace filtering: alerts with namespace label only sent to matching subscribers
- [ ] Alerts without namespace label sent to all subscribers

### Settings
- [ ] `GET /api/v1/alerts/settings` returns config with SMTP password masked
- [ ] `PUT /api/v1/alerts/settings` updates in-memory config, audit logged
- [ ] Empty password field in PUT preserves existing password
- [ ] Webhook token displayed masked with reveal option
- [ ] Settings page shows Alertmanager receiver YAML snippet

### Frontend
- [ ] AlertBanner displays active alert count in layout (below TopBar)
- [ ] AlertBanner updates in real-time via WebSocket
- [ ] Active Alerts page shows firing alerts with severity color coding
- [ ] History page shows paginated alert history with filters
- [ ] Alert Rules page lists PrometheusRule CRDs
- [ ] Create/edit rules via Monaco YAML editor
- [ ] Settings page has SMTP form with test email button
- [ ] Warning banner about in-memory persistence

---

## Testing Strategy

### Backend Unit Tests (`backend/internal/alerting/alerting_test.go`)

- Webhook handler: valid payload, invalid token, malformed JSON, body too large, deduplication, status transitions
- Alert store: record, list, active, resolve, prune, cap eviction, concurrent access
- Email notifier: rate limiting (global + per-alert), cooldown bypass for resolved, digest trigger
- Rules manager: CRD availability check, list with label selector, create with label injection, delete with label guard
- Settings handler: GET masks password, PUT preserves empty password, PUT updates config

### Smoke Test Additions

Add to the homelab smoke test procedure:
1. Start backend with `KUBECENTER_ALERTING_ENABLED=true KUBECENTER_ALERTING_WEBHOOKTOKEN=test-webhook-token-for-smoke`
2. Verify webhook: `curl -X POST http://localhost:8080/api/v1/alerts/webhook -H "Authorization: Bearer test-webhook-token-for-smoke" -H "Content-Type: application/json" -d '{"version":"4","groupKey":"test","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"TestAlert","severity":"warning","namespace":"default"},"annotations":{"summary":"Test alert"},"startsAt":"2026-03-14T00:00:00Z","endsAt":"0001-01-01T00:00:00Z","fingerprint":"abc123"}]}'` → 200
3. Verify active alerts: `GET /api/v1/alerts` → contains TestAlert
4. Verify webhook auth: `POST /api/v1/alerts/webhook` without token → 401
5. Verify alert rules: `GET /api/v1/alerts/rules` → list (empty if no KubeCenter-managed rules)
6. Verify settings: `GET /api/v1/alerts/settings` → returns config
7. If SMTP configured, verify test email: `POST /api/v1/alerts/test` → 200

---

## Security Considerations

- Webhook token verified with `crypto/subtle.ConstantTimeCompare` (timing-safe)
- Webhook endpoint has its own rate limiter (300/min), not shared with auth endpoints
- SMTP password never logged, never returned unmasked in API responses
- SMTP password only from env var or UI (in-memory), never written to config files
- All alert rule CRUD uses impersonated dynamic client (k8s RBAC enforced)
- PrometheusRule deletion refused for rules not managed by KubeCenter (label check)
- Email body rendered with `html/template` (auto-escapes HTML injection from alert labels/annotations)
- Webhook body limited to 1MB to prevent memory exhaustion
- Alert store capped at 10,000 entries to prevent unbounded memory growth
- Test email endpoint rate limited to 1/min per user (prevent abuse)
- Audit logging on all write operations: rule CRUD, settings update, test email

---

## Implementation Order

1. Config (`config.go`, `defaults.go`) — foundation for everything
2. Audit actions (`logger.go`) — needed before any handler code
3. Alert store (`store.go`) — core data structure
4. Webhook receiver (`webhook.go`) — processes incoming alerts
5. WS integration (`events.go`, `hub.go`) — real-time broadcasting
6. Email notifier (`notifier.go`, templates) — SMTP sending
7. Rules manager (`rules.go`) — PrometheusRule CRD CRUD
8. Handler (`handler.go`) — HTTP handlers wiring store + notifier + rules
9. Server wiring (`server.go`, `routes.go`, `main.go`) — register routes
10. RBAC extension (`access.go`) — monitoring.coreos.com API group
11. Frontend nav + AlertBanner (`constants.ts`, `_layout.tsx`, `AlertBanner.tsx`)
12. Frontend alerts page (`AlertsPage.tsx`, `routes/alerting/index.tsx`)
13. Frontend rules page (`AlertRulesPage.tsx`, `routes/alerting/rules.tsx`)
14. Frontend settings page (`AlertSettings.tsx`, `routes/alerting/settings.tsx`)
15. Tests (`alerting_test.go`)
16. Smoke test against homelab
