# feat: Flux Notifications — Provider, Alert & Receiver Management

## Overview

Full read + CRUD support for Flux CD Notification Controller CRDs — Provider (`v1beta3`), Alert (`v1beta3`), and Receiver (`v1`). New "Notifications" tab in the GitOps section with a tabbed list view (Providers / Alerts / Receivers), real-time WebSocket updates, and create/edit/delete/suspend actions via user impersonation. Graceful degradation when the notification-controller is not installed.

## Problem Statement

k8sCenter manages Flux CD Kustomizations and HelmReleases but has no visibility into the notification pipeline — the Providers (where notifications go), Alerts (which events trigger them), and Receivers (inbound webhooks that trigger reconciliation). Users must use `kubectl` to manage notification routing, with no UI for viewing relationships between Providers and Alerts or managing the notification topology.

## Architecture

### Design Decisions

| Decision | Choice | Why |
|---|---|---|
| Package | New `internal/notification/` package | Separate concern from gitops (Applications/AppSets). Follows policy package pattern. |
| Discovery | Add `NotificationAvailable bool` to `ToolDetail`. Probe `notification.toolkit.fluxcd.io/v1beta3` in existing `discoverFlux()`. Callback signature unchanged. | Notification availability is a property of the Flux installation (like controllers list). Avoids breaking the 2-arg callback with positional booleans. |
| CRD versions | `notification.toolkit.fluxcd.io/v1beta3` (Provider, Alert), `notification.toolkit.fluxcd.io/v1` (Receiver) | Latest stable Flux versions. No v1beta2 fallback (matches existing pattern for Kustomization/HelmRelease). |
| WS kind names | `"flux-providers"`, `"flux-alerts"`, `"flux-receivers"` | Avoids collision with existing `"alerts"` kind in `alwaysAllowKinds` (Alertmanager). Critical for RBAC correctness. |
| Caching | Separate singleflight + 30s TTL cache from gitops handler | Independent data, independent lifecycle. |
| CRUD | Full CRUD for Provider, Alert, Receiver | Users need to create notification routing through the UI. |
| Suspend | Provider and Alert only (not Receiver) | Receiver CRD v1 does not have `spec.suspend` field. |
| Multi-cluster | `ClusterRouter` field on handler, same as policy handler | Notification resources must respect X-Cluster-ID context. |
| Provider create | Flat form: name, namespace, type (dropdown), address, channel, secretRef — all fields visible | Flux controller validates required fields per type. No frontend type-mapping needed. Enhance to dynamic form later if users struggle. |
| Secret handling | Free-text secretRef name input (not a picker) | Avoids requiring `list secrets` RBAC. Show validation warning if secret doesn't exist after creation. |
| Managed-by label | All CRUD-created resources get `app.kubernetes.io/managed-by: kubecenter` label | Prevents accidental edits to GitOps-managed resources. Enables "show only UI-created" filtering. Follows `alerting/rules.go` pattern. |
| Input validation | `MaxBytesReader` on POST/PUT bodies, K8s name regex, required field checks server-side | Security-relevant. Flux CRD rejects bad input but we should fail fast with clear errors. |
| Detail view | Inline expandable rows (not separate routes) | Three resource types on one page — separate routes per resource would be excessive. |
| Tab URL state | Query param `?tab=providers\|alerts\|receivers` | Enables deep-linking without needing 3 separate routes. |
| Audit | Reuse generic `ActionCreate`/`ActionUpdate`/`ActionDelete` with `ResourceKind` set to `"Provider"`/`"Alert"`/`"Receiver"`. Add only `ActionNotificationSuspend` for suspend toggle. | Follows existing convention. Audit entries already have `ResourceKind` field for filtering. |
| Rate limiting | Share existing `yamlRateLimiter` (30 req/min) for write endpoints | Consistent with other CRUD endpoint groups. |

### Types

```go
// backend/internal/notification/types.go

type NormalizedProvider struct {
    Name       string            `json:"name"`
    Namespace  string            `json:"namespace"`
    Type       string            `json:"type"`       // "slack", "discord", "github", "generic", etc.
    Channel    string            `json:"channel"`
    Address    string            `json:"address"`    // may be empty if stored in secret
    SecretRef  string            `json:"secretRef"`  // secret name (never the value)
    Suspend    bool              `json:"suspend"`
    Status     string            `json:"status"`     // "Ready", "Not Ready", "Suspended"
    Message    string            `json:"message"`    // condition message
    CreatedAt  string            `json:"createdAt"`
}

type NormalizedAlert struct {
    Name          string            `json:"name"`
    Namespace     string            `json:"namespace"`
    ProviderRef   string            `json:"providerRef"`    // provider name in same namespace
    EventSeverity string            `json:"eventSeverity"`  // "info" or "error"
    EventSources  []EventSourceRef  `json:"eventSources"`
    InclusionList []string          `json:"inclusionList"`
    ExclusionList []string          `json:"exclusionList"`
    Suspend       bool              `json:"suspend"`
    Status        string            `json:"status"`
    Message       string            `json:"message"`
    CreatedAt     string            `json:"createdAt"`
}

type EventSourceRef struct {
    Kind        string            `json:"kind"`       // Kustomization, HelmRelease, GitRepository, etc.
    Name        string            `json:"name"`       // specific name or "*"
    Namespace   string            `json:"namespace"`  // empty = same as Alert
    MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

type NormalizedReceiver struct {
    Name        string            `json:"name"`
    Namespace   string            `json:"namespace"`
    Type        string            `json:"type"`       // "github", "gitlab", "generic", etc.
    Resources   []EventSourceRef  `json:"resources"`  // resources to reconcile
    WebhookPath string            `json:"webhookPath"` // from status, e.g. "/hook/sha256-abc"
    SecretRef   string            `json:"secretRef"`
    Status      string            `json:"status"`
    Message     string            `json:"message"`
    CreatedAt   string            `json:"createdAt"`
}

type NotificationStatus struct {
    Available       bool   `json:"available"`
    ProviderCount   int    `json:"providerCount"`
    AlertCount      int    `json:"alertCount"`
    ReceiverCount   int    `json:"receiverCount"`
}
```

```typescript
// frontend/lib/notification-types.ts

export interface NormalizedProvider {
  name: string;
  namespace: string;
  type: string;
  channel: string;
  address: string;
  secretRef: string;
  suspend: boolean;
  status: string;
  message: string;
  createdAt: string;
}

export interface NormalizedAlert {
  name: string;
  namespace: string;
  providerRef: string;
  eventSeverity: string;
  eventSources: EventSourceRef[];
  inclusionList: string[];
  exclusionList: string[];
  suspend: boolean;
  status: string;
  message: string;
  createdAt: string;
}

export interface EventSourceRef {
  kind: string;
  name: string;
  namespace: string;
  matchLabels?: Record<string, string>;
}

export interface NormalizedReceiver {
  name: string;
  namespace: string;
  type: string;
  resources: EventSourceRef[];
  webhookPath: string;
  secretRef: string;
  status: string;
  message: string;
  createdAt: string;
}

export interface NotificationStatus {
  available: boolean;
  providerCount: number;
  alertCount: number;
  receiverCount: number;
}
```

### API Endpoints

All under `/api/v1/gitops/notifications/`:

| Method | Path | Description | Auth |
|---|---|---|---|
| GET | `/status` | Notification controller availability + counts | User token |
| GET | `/providers[?namespace=]` | List Providers (RBAC-filtered, optional namespace filter) | User token |
| GET | `/alerts[?namespace=]` | List Alerts (RBAC-filtered, optional namespace filter) | User token |
| GET | `/receivers[?namespace=]` | List Receivers (RBAC-filtered, optional namespace filter) | User token |
| POST | `/providers` | Create Provider (user impersonation) | User token + CSRF |
| PUT | `/providers/{namespace}/{name}` | Update Provider | User token + CSRF |
| DELETE | `/providers/{namespace}/{name}` | Delete Provider | User token + CSRF |
| POST | `/providers/{namespace}/{name}/suspend` | Toggle suspend | User token + CSRF |
| POST | `/alerts` | Create Alert | User token + CSRF |
| PUT | `/alerts/{namespace}/{name}` | Update Alert | User token + CSRF |
| DELETE | `/alerts/{namespace}/{name}` | Delete Alert | User token + CSRF |
| POST | `/alerts/{namespace}/{name}/suspend` | Toggle suspend | User token + CSRF |
| POST | `/receivers` | Create Receiver | User token + CSRF |
| PUT | `/receivers/{namespace}/{name}` | Update Receiver | User token + CSRF |
| DELETE | `/receivers/{namespace}/{name}` | Delete Receiver | User token + CSRF |

### GVR Constants

```go
var (
    FluxProviderGVR = schema.GroupVersionResource{
        Group: "notification.toolkit.fluxcd.io", Version: "v1beta3", Resource: "providers",
    }
    FluxAlertGVR = schema.GroupVersionResource{
        Group: "notification.toolkit.fluxcd.io", Version: "v1beta3", Resource: "alerts",
    }
    FluxReceiverGVR = schema.GroupVersionResource{
        Group: "notification.toolkit.fluxcd.io", Version: "v1", Resource: "receivers",
    }
)
```

### Provider Create Form

Flat form — all fields always visible. The Flux controller validates required fields per type and reports errors via CRD conditions. No frontend type-mapping needed.

**Fields:** `name`, `namespace`, `type` (flat dropdown of all 29+ types), `address`, `channel`, `secretRef`

The type dropdown is a simple alphabetical list. Users who know they want Slack will type "s" to jump. No category grouping — the Flux docs are the reference for which fields each type needs. If the user leaves a required field empty, the CRD status will show the error message, which we surface in the table.

### WebSocket Event Kinds

| CRD | WS Kind | API Group (for RBAC) |
|---|---|---|
| Provider | `flux-providers` | `notification.toolkit.fluxcd.io` |
| Alert | `flux-alerts` | `notification.toolkit.fluxcd.io` |
| Receiver | `flux-receivers` | `notification.toolkit.fluxcd.io` |

### File Map

**New backend files:**
- `backend/internal/notification/types.go` — Normalized types, GVR constants
- `backend/internal/notification/flux_notifications.go` — List/normalize/CRUD functions for Provider, Alert, Receiver
- `backend/internal/notification/handler.go` — HTTP handler with singleflight + cache, RBAC filtering
- `backend/internal/notification/flux_notifications_test.go` — Unit tests

**Modified backend files:**
- `backend/internal/gitops/discovery.go` — Add `notification.toolkit.fluxcd.io` CRD probing, update callback signature
- `backend/internal/gitops/types.go` — Update `DiscoveryChangeCallback` to include `notificationAvailable`
- `backend/internal/server/routes.go` — Register notification routes
- `backend/internal/server/server.go` — Add `NotificationHandler` field to Server struct + ServerDeps
- `backend/cmd/kubecenter/main.go` — Wire notification handler, CRD watches, WebSocket kinds
- `backend/internal/audit/logger.go` — Add notification action constants
- `backend/internal/websocket/events.go` — No changes needed (dynamic registration via `RegisterAllowedKind`)

**New frontend files:**
- `frontend/lib/notification-types.ts` — TypeScript interfaces
- `frontend/routes/gitops/notifications.tsx` — Route page with SubNav + tab routing
- `frontend/islands/FluxProviders.tsx` — Provider list + create/edit forms
- `frontend/islands/FluxAlerts.tsx` — Alert list + create/edit forms
- `frontend/islands/FluxReceivers.tsx` — Receiver list + create/edit forms
- `frontend/components/ui/NotificationBadges.tsx` — ProviderTypeBadge, SeverityBadge, StatusBadge

**Modified frontend files:**
- `frontend/lib/constants.ts` — Add "Notifications" tab to GitOps section
- `frontend/lib/api.ts` — No changes (generic `apiGet`/`apiPost`/`apiPut`/`apiDelete` already exist)
- `frontend/islands/CommandPalette.tsx` — Add "GitOps Notifications" quick action

---

## Implementation Phases

### Phase 1: Backend — New Package + Discovery + Handler + Wiring (5 files max per sub-phase)

#### Phase 1A: Types + Normalize + CRUD functions (3 new files)

**Files:**
1. `backend/internal/notification/types.go` — NEW: types, GVR constants
2. `backend/internal/notification/flux_notifications.go` — NEW: list/normalize/CRUD functions
3. `backend/internal/notification/handler.go` — NEW: HTTP handler + cache + RBAC

**Details:**

Create the `notification` package with:
- GVR constants for Provider, Alert, Receiver
- `ListProviders(ctx, dynClient)` → `[]NormalizedProvider`
- `ListAlerts(ctx, dynClient)` → `[]NormalizedAlert`
- `ListReceivers(ctx, dynClient)` → `[]NormalizedReceiver`
- `NormalizeProvider(obj *unstructured.Unstructured)` → `NormalizedProvider`
- `NormalizeAlert(obj *unstructured.Unstructured)` → `NormalizedAlert`
- `NormalizeReceiver(obj *unstructured.Unstructured)` → `NormalizedReceiver`
- Reuse `mapFluxConditions()` pattern from `gitops/flux.go` for status extraction
- CRUD: `CreateProvider`, `UpdateProvider`, `DeleteProvider`, `SuspendProvider` (same for Alert, Receiver minus suspend)
- All writes use `DynamicClientForUser()` for impersonation
- All CRUD-created resources get `app.kubernetes.io/managed-by: kubecenter` label
- Input validation: `MaxBytesReader` (1KB for actions, 8KB for create/update), K8s name regex, required field checks

Handler:
- `singleflight.Group` + `sync.RWMutex` + 30s TTL cache (one cache for all three types — small datasets, always fetched on the same page)
- `InvalidateCache()` exported for CRD watch callbacks
- `HandleStatus`, `HandleListProviders`, `HandleListAlerts`, `HandleListReceivers` (all support `?namespace=` query param)
- `HandleCreateProvider`, `HandleUpdateProvider`, `HandleDeleteProvider`, `HandleSuspendProvider`
- Same pattern for alerts and receivers (minus suspend for receivers)
- RBAC filtering via `AccessChecker.CanAccessGroupResource()` per namespace
- Audit logging on all writes using generic `ActionCreate`/`ActionUpdate`/`ActionDelete` with `ResourceKind`; `ActionNotificationSuspend` for suspend toggle

**Verification:**
```bash
go vet ./internal/notification/...
go build ./internal/notification/...
```

#### Phase 1B: Discovery + Wiring + Routes + Tests (5 files)

**Files:**
1. `backend/internal/gitops/discovery.go` — MODIFY: add notification CRD probing
2. `backend/internal/gitops/types.go` — MODIFY: add `NotificationAvailable bool` to `ToolDetail`
3. `backend/internal/server/routes.go` — MODIFY: register notification routes
4. `backend/internal/server/server.go` — MODIFY: add `NotificationHandler` to Server + Deps
5. `backend/cmd/kubecenter/main.go` — MODIFY: create handler, wire discovery, CRD watches

**Discovery extension (no callback signature change):**
- Add `NotificationAvailable bool` to `ToolDetail` struct
- In `discoverFlux()`, probe `notification.toolkit.fluxcd.io/v1beta3` for Provider/Alert kinds
- Add `"notification"` to controller name enumeration (line 138)
- Set `fluxDetail.NotificationAvailable = true` if CRDs found
- Existing 2-arg callback fires on Flux state changes — `main.go` checks `fluxDetail.NotificationAvailable`

**Verification:**
```bash
go vet ./...
go build ./cmd/kubecenter/
```

#### Phase 1C: Unit Tests (1-2 files)

**Files:**
1. `backend/internal/notification/flux_notifications_test.go` — NEW: comprehensive tests

**Test coverage (minimum):**
- `TestNormalizeProvider` — ready/not-ready/suspended states
- `TestNormalizeAlert` — with various event source configurations, inclusion/exclusion patterns
- `TestNormalizeReceiver` — with webhookPath populated/empty
- `TestListProviders_RBACFiltering` — verifies namespace filtering
- `TestCreateProvider_Validation` — invalid name, missing type, body size limits
- `TestCreateProvider_ManagedByLabel` — verifies label is set on created resources
- `TestSuspendProvider` — toggle suspend on/off
- `TestSuspendReceiver_Rejected` — receivers don't support suspend
- `TestDiscovery_NotificationAvailable` — notification CRD probing sets ToolDetail field

**Verification:**
```bash
go test ./internal/notification/... -v
go vet ./...
```

### Phase 2: Frontend — Types, Route, Islands (5 files)

#### Phase 2A: Types + Route + Provider Island (5 files)

**Files:**
1. `frontend/lib/notification-types.ts` — NEW: TypeScript interfaces
2. `frontend/routes/gitops/notifications.tsx` — NEW: route page with tab routing
3. `frontend/islands/FluxProviders.tsx` — NEW: Provider list + create/edit/delete/suspend
4. `frontend/components/ui/NotificationBadges.tsx` — NEW: badge components
5. `frontend/lib/constants.ts` — MODIFY: add Notifications tab to GitOps section

**Details:**

Route (`/gitops/notifications`):
- Renders SubNav + active tab island based on `?tab=` query param (default: `providers`)
- SSR selects which island to render — no client-side tab switching needed
```tsx
export default define.page(function NotificationsPage(ctx) {
    const tab = ctx.url.searchParams.get("tab") ?? "providers";
    return (
        <>
            <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
            <NotificationTabs activeTab={tab} />
            {tab === "providers" && <FluxProviders />}
            {tab === "alerts" && <FluxAlerts />}
            {tab === "receivers" && <FluxReceivers />}
        </>
    );
});
```

Island (`FluxProviders`):
- Filterable/sortable table: Name, Namespace, Type, Channel/Address, Status, CreatedAt, Actions
- Actions: Edit (modal), Delete (confirm dialog), Suspend/Resume
- Create button → flat form modal: name, namespace, type (dropdown), address, channel, secretRef
- Empty state: "No providers configured" with Create button
- Degradation: if status endpoint returns `available === false`, show banner + disable CRUD
- WebSocket: `useWsRefetch(fetchProviders, [["flux-providers-sub", "flux-providers", ""]], 1000)`

Badges:
- `ProviderTypeBadge` — label for provider type
- `StatusBadge` — Ready (green), Not Ready (red), Suspended (yellow)
- `SeverityBadge` — Info (blue), Error (red)

Constants update: Add `{ label: "Notifications", href: "/gitops/notifications" }` to GitOps tabs.

**Verification:**
```bash
cd frontend && deno lint
cd frontend && deno task build
```

#### Phase 2B: Alert + Receiver Islands + Command Palette (4 files)

**Files:**
1. `frontend/islands/FluxAlerts.tsx` — NEW: Alert list + create/edit/delete/suspend
2. `frontend/islands/FluxReceivers.tsx` — NEW: Receiver list + create/edit/delete
3. `frontend/islands/CommandPalette.tsx` — MODIFY: add notification quick action
4. `frontend/routes/gitops/notifications.tsx` — MODIFY: wire remaining islands if needed

**Details:**

Alert create form:
- Flat form: `name`, `namespace`, `providerRef` (dropdown of providers in selected namespace), `eventSeverity` (info/error toggle), `eventSources` (text inputs for kind/name pairs), optional `inclusionList`/`exclusionList` (comma-separated text inputs)

Receiver create form:
- Flat form: `name`, `namespace`, `type` (dropdown of 12 receiver types), `resources` (text inputs for kind/name pairs), `secretRef`
- After creation, show `webhookPath` from status with copy button + help text for constructing full URL

Command palette: Add "GitOps Notifications" action → navigates to `/gitops/notifications`

**Verification:**
```bash
cd frontend && deno lint
cd frontend && deno task build
```

### Phase 3: E2E Tests + Polish (2-3 files)

**Files:**
1. `e2e/flux-notifications.spec.ts` — NEW: Playwright tests
2. Any bug fixes discovered during E2E

**E2E test scenarios:**
- Navigate to GitOps > Notifications when notification-controller is NOT installed → degradation banner shown
- Navigate to GitOps > Notifications when installed → Provider/Alert/Receiver tabs visible
- Tab switching via URL `?tab=` param persists on refresh
- Create a Provider → appears in table
- Create an Alert referencing the Provider → appears in table
- Suspend a Provider → status updates to "Suspended"
- Delete a Provider → removed from table
- CRUD validation: create with invalid name → error shown

**Verification:**
```bash
make test-e2e
go vet ./...
go test ./...
cd frontend && deno lint
```

---

## Edge Cases

| Case | Handling |
|---|---|
| Notification controller not installed | `status.available = false`, UI shows install banner, CRUD disabled |
| Notification controller installed but unhealthy | CRDs present so `available = true`, but list calls may fail — show error toast |
| Provider references non-existent Secret | Provider shows "Not Ready" status with condition message from CRD |
| Alert references non-existent Provider | Alert shows "Not Ready" with validation error message |
| Receiver webhookPath empty (not yet reconciled) | Show "Pending" status, poll via WS for status update |
| Cross-namespace event sources | Alert form restricts to RBAC-accessible namespaces; raw references in existing Alerts shown as-is |
| RBAC-restricted user | Only sees notification resources in permitted namespaces |
| Remote cluster selected | `ClusterRouter` resolves correct dynamic client; no informer watches (direct API) |
| Very large number of Providers (100+) | Client-side pagination in table (matches existing pattern) |
| Invalid regex in inclusion/exclusion list | Server-side validation before creating Alert CRD |

## Security Considerations

- All writes use user impersonation via `DynamicClientForUser()` — never service account
- Secret values never exposed: only `secretRef.name` is shown in normalized types
- RBAC filtering on all list endpoints via `CanAccessGroupResource()`
- CSRF protection via `X-Requested-With` header on all state-changing endpoints
- Audit logging for all create/update/delete/suspend operations
- WebSocket subscriptions gated by RBAC (per-kind, per-namespace)
- Rate limiting on write endpoints (30 req/min shared bucket)
- Input validation: `MaxBytesReader` on POST/PUT bodies, K8s name regex, required field checks
- CRUD-created resources labeled `app.kubernetes.io/managed-by: kubecenter`

## References

### Internal
- `backend/internal/gitops/flux.go` — Flux CRD reading pattern
- `backend/internal/gitops/discovery.go` — Discovery loop + callback
- `backend/internal/gitops/handler.go` — singleflight + cache + RBAC pattern
- `backend/internal/policy/handler.go` — Multi-engine handler with ClusterRouter
- `backend/internal/alerting/rules.go` — CRD CRUD with managed-by labels
- `backend/internal/websocket/events.go` — Dynamic kind registration
- `backend/cmd/kubecenter/main.go:471-518` — CRD watch wiring pattern

### External
- [Flux Notification Controller](https://fluxcd.io/flux/components/notification/)
- [Provider CRD (v1beta3)](https://fluxcd.io/flux/components/notification/providers/)
- [Alert CRD (v1beta3)](https://fluxcd.io/flux/components/notification/alerts/)
- [Receiver CRD (v1)](https://fluxcd.io/flux/components/notification/receivers/)
- [Flux Events](https://fluxcd.io/flux/components/notification/events/)
- [notification-controller GitHub](https://github.com/fluxcd/notification-controller)
- [Flux Monitoring Alerts Guide](https://fluxcd.io/flux/monitoring/alerts/)
