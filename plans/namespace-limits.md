# Namespace Limits Management

Feature: Unified ResourceQuota + LimitRange management with utilization dashboard, tiered wizard, and overage notifications.

**Priority:** #4 on roadmap (after Notification Center, Git commit display, Diff view)
**Design Date:** 2026-04-10
**Revised:** 2026-04-10 (incorporated review findings)

---

## Overview

Admin-first feature for managing namespace resource limits. Combines ResourceQuota (aggregate caps) and LimitRange (per-object defaults/bounds) into a unified "Namespace Limits" surface. Includes:

- Dashboard showing all namespaces with quota posture and utilization (slide-out panel for details)
- Tiered wizard (presets + advanced toggle) for creating both objects in one flow
- Background checker with Notification Center integration for overage warnings
- Threshold system: 80% warning, 95% critical (global defaults with per-quota annotation overrides)

---

## Architecture

```
┌─────────────────┐     ┌──────────────────────────────────────┐
│  Informers      │────▶│  limits.Handler                      │
│  (RQ + LR)      │     │  - singleflight + cache (30s)        │
└─────────────────┘     │  - aggregation, utilization          │
                        │  - threshold checking                 │
                        │  - HTTP endpoints                     │
┌─────────────────┐     └────────────────┬─────────────────────┘
│  Background     │                      │
│  Checker (5min) │──────────────────────┘
└────────┬────────┘                      │
         │                               ▼
         │                      ┌─────────────────────┐
         └─────────────────────▶│  Notification       │
                                │  Center (dispatch)  │
                                └─────────────────────┘
```

**Key decisions:**
- Package named `limits` (matches feature name, consistent with `policy/`, `gitops/`)
- Business logic lives in Handler (matches existing patterns, no separate Service)
- Singleflight + 30s cache for dashboard endpoint (prevents burst traffic issues)
- No new database tables — thresholds stored as annotations
- Background checker monitors local cluster only (5-minute interval)
- Multi-cluster support via ClusterRouter for dashboard/detail
- Slide-out panel for namespace detail (no separate route)

---

## Phase 1: Backend Foundation

### Step 1: Types and Handler

**Files:**
- `backend/internal/limits/types.go`
- `backend/internal/limits/handler.go`
- `backend/internal/limits/handler_test.go`

**Types (`types.go`):**

```go
package limits

import (
    "k8s.io/apimachinery/pkg/api/resource"
)

type ThresholdStatus string

const (
    ThresholdOK       ThresholdStatus = "ok"
    ThresholdWarning  ThresholdStatus = "warning"
    ThresholdCritical ThresholdStatus = "critical"
)

const (
    DefaultWarnThreshold     = 0.80
    DefaultCriticalThreshold = 0.95
    
    AnnotationWarnThreshold     = "k8scenter.io/warn-threshold"
    AnnotationCriticalThreshold = "k8scenter.io/critical-threshold"
)

// NamespaceSummary is the dashboard row for one namespace
type NamespaceSummary struct {
    Namespace          string          `json:"namespace"`
    HasQuota           bool            `json:"hasQuota"`
    HasLimitRange      bool            `json:"hasLimitRange"`
    CPUUsedPercent     float64         `json:"cpuUsedPercent,omitempty"`
    MemoryUsedPercent  float64         `json:"memoryUsedPercent,omitempty"`
    HighestUtilization float64         `json:"highestUtilization"`
    Status             ThresholdStatus `json:"status"`
    QuotaCount         int             `json:"quotaCount"`
    LimitRangeCount    int             `json:"limitRangeCount"`
}

// NamespaceLimits is the detail view for one namespace
type NamespaceLimits struct {
    Namespace   string               `json:"namespace"`
    Quotas      []NormalizedQuota    `json:"quotas"`
    LimitRanges []NormalizedLimitRange `json:"limitRanges"`
}

// NormalizedQuota wraps a ResourceQuota with computed utilization
type NormalizedQuota struct {
    Name              string                         `json:"name"`
    Utilization       map[string]ResourceUtilization `json:"utilization"`
    WarnThreshold     float64                        `json:"warnThreshold"`
    CriticalThreshold float64                        `json:"criticalThreshold"`
}

// ResourceUtilization tracks usage for one resource dimension
type ResourceUtilization struct {
    Used       string          `json:"used"`
    Hard       string          `json:"hard"`
    Percentage float64         `json:"percentage"`
    Status     ThresholdStatus `json:"status"`
}

// NormalizedLimitRange abstracts away k8s API details
type NormalizedLimitRange struct {
    Name   string           `json:"name"`
    Limits []LimitRangeItem `json:"limits"`
}

// LimitRangeItem is one limit type within a LimitRange
type LimitRangeItem struct {
    Type                 string            `json:"type"` // Container, Pod, PersistentVolumeClaim
    Default              map[string]string `json:"default,omitempty"`
    DefaultRequest       map[string]string `json:"defaultRequest,omitempty"`
    Min                  map[string]string `json:"min,omitempty"`
    Max                  map[string]string `json:"max,omitempty"`
    MaxLimitRequestRatio map[string]string `json:"maxLimitRequestRatio,omitempty"`
}

// QuotaThresholdEvent is dispatched to Notification Center
type QuotaThresholdEvent struct {
    Namespace   string          `json:"namespace"`
    QuotaName   string          `json:"quotaName"`
    Resource    string          `json:"resource"`
    Status      ThresholdStatus `json:"status"`
    UsedPercent float64         `json:"usedPercent"`
    Threshold   float64         `json:"threshold"`
    Used        string          `json:"used"`
    Hard        string          `json:"hard"`
}

// InformerSource provides access to ResourceQuota and LimitRange listers
type InformerSource interface {
    ResourceQuotas() ResourceQuotaLister
    LimitRanges() LimitRangeLister
}
```

**Handler (`handler.go`):**

```go
package limits

import (
    "context"
    "log/slog"
    "net/http"
    "sync"
    "time"
    
    "github.com/go-chi/chi/v5"
    "golang.org/x/sync/singleflight"
    
    corev1 "k8s.io/api/core/v1"
)

const cacheTTL = 30 * time.Second

type Handler struct {
    Informers     InformerSource
    AccessChecker AccessChecker
    Logger        *slog.Logger
    
    // Singleflight + cache (per policy/gitops patterns)
    fetchGroup singleflight.Group
    cacheMu    sync.RWMutex
    cachedData *cachedLimitsData
    cacheTime  time.Time
}

type cachedLimitsData struct {
    summaries []NamespaceSummary
}

func NewHandler(informers InformerSource, accessChecker AccessChecker, logger *slog.Logger) *Handler

// HandleStatus handles GET /limits/status (discovery endpoint)
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request)

// HandleListNamespaces handles GET /limits/namespaces (dashboard)
func (h *Handler) HandleListNamespaces(w http.ResponseWriter, r *http.Request)

// HandleGetNamespace handles GET /limits/namespaces/{namespace} (detail)
func (h *Handler) HandleGetNamespace(w http.ResponseWriter, r *http.Request)

// Internal methods
func (h *Handler) fetchSummaries(ctx context.Context) ([]NamespaceSummary, error)
func (h *Handler) filterByRBAC(ctx context.Context, user string, summaries []NamespaceSummary) []NamespaceSummary
func (h *Handler) computeUtilization(quota *corev1.ResourceQuota) map[string]ResourceUtilization
func (h *Handler) checkThresholds(quota *corev1.ResourceQuota) ThresholdStatus
func (h *Handler) normalizeQuota(quota *corev1.ResourceQuota) NormalizedQuota
func (h *Handler) normalizeLimitRange(lr *corev1.LimitRange) NormalizedLimitRange
func ParseThresholdAnnotations(quota *corev1.ResourceQuota) (warn, critical float64)
```

**Routes (add to `routes.go`):**

```go
r.Route("/limits", func(lr chi.Router) {
    lr.Get("/status", s.LimitsHandler.HandleStatus)
    lr.Get("/namespaces", s.LimitsHandler.HandleListNamespaces)
    lr.Get("/namespaces/{namespace}", s.LimitsHandler.HandleGetNamespace)
})
```

**Tests (`handler_test.go`):**
- `TestCheckThresholds` — 80/95 defaults, annotation overrides, edge cases (0%, 100%, over 100%)
- `TestParseThresholdAnnotations` — valid, invalid, missing annotations
- `TestComputeUtilization` — percentage calculation accuracy
- `TestFilterByRBAC` — RBAC filtering removes unauthorized namespaces
- `TestCacheInvalidation` — singleflight dedupes, cache expires after TTL
- `TestHandleListNamespaces_Errors` — informer unavailable, empty cluster

---

## Phase 2: Background Checker

### Step 2: Checker with Notification Integration

**Files:**
- `backend/internal/limits/checker.go`
- `backend/internal/limits/checker_test.go`

**Checker (`checker.go`):**

```go
package limits

import (
    "context"
    "log/slog"
    "sync"
    "time"
    
    "github.com/maulepilot117/k8scenter/backend/internal/notifications"
)

const (
    DefaultCheckInterval = 5 * time.Minute
    EventTypeThreshold   = "limits.threshold_crossed"
)

type Checker struct {
    handler  *Handler
    notifier *notifications.Service
    interval time.Duration
    logger   *slog.Logger
    
    mu        sync.Mutex
    lastState map[string]ThresholdStatus // key: "namespace:quotaName:resource"
    
    stopCh chan struct{}
    wg     sync.WaitGroup
}

func NewChecker(handler *Handler, notifier *notifications.Service, interval time.Duration, logger *slog.Logger) *Checker

// Start begins the background checking loop (non-blocking)
func (c *Checker) Start(ctx context.Context)

// Stop gracefully shuts down the checker
func (c *Checker) Stop()

// check runs one check cycle across all quotas
func (c *Checker) check(ctx context.Context)

// dispatchIfChanged sends notification only when status changes
func (c *Checker) dispatchIfChanged(key string, current ThresholdStatus, event QuotaThresholdEvent)

// stateKey builds composite key with colon delimiter
func stateKey(namespace, quotaName, resource string) string {
    return namespace + ":" + quotaName + ":" + resource
}
```

**Notification event type (add to existing events):**

```go
// Register in notifications/events.go or similar
const EventTypeLimitsThreshold = "limits.threshold_crossed"

var LimitsThresholdEventMeta = EventTypeMeta{
    Type:        EventTypeLimitsThreshold,
    DisplayName: "Quota Threshold Crossed",
    Description: "Namespace approaching or exceeding resource quota",
    Category:    "platform",
}
```

**Server integration (`server.go`):**
- Add `LimitsChecker *limits.Checker` to Server struct
- Start in `Run()`: `s.LimitsChecker.Start(ctx)`
- Stop in shutdown: `s.LimitsChecker.Stop()`

**Tests (`checker_test.go`):**
- `TestCheckerStartStop` — lifecycle, context cancellation
- `TestDispatchIfChanged` — only fires on state change
- `TestStateKeyFormat` — uses colon delimiter
- `TestCheckerErrorHandling` — logs and continues on per-namespace errors

---

## Phase 3: Frontend

### Step 3: TypeScript Types and API Client

**Files:**
- `frontend/lib/limits-types.ts`
- `frontend/lib/api.ts` (modify)

**Types (`limits-types.ts`):**

```typescript
export type ThresholdStatus = "ok" | "warning" | "critical";

export interface NamespaceSummary {
  namespace: string;
  hasQuota: boolean;
  hasLimitRange: boolean;
  cpuUsedPercent?: number;
  memoryUsedPercent?: number;
  highestUtilization: number;
  status: ThresholdStatus;
  quotaCount: number;
  limitRangeCount: number;
}

export interface NamespaceLimits {
  namespace: string;
  quotas: NormalizedQuota[];
  limitRanges: NormalizedLimitRange[];
}

export interface NormalizedQuota {
  name: string;
  utilization: Record<string, ResourceUtilization>;
  warnThreshold: number;
  criticalThreshold: number;
}

export interface ResourceUtilization {
  used: string;
  hard: string;
  percentage: number;
  status: ThresholdStatus;
}

export interface NormalizedLimitRange {
  name: string;
  limits: LimitRangeItem[];
}

export interface LimitRangeItem {
  type: "Container" | "Pod" | "PersistentVolumeClaim";
  default?: Record<string, string>;
  defaultRequest?: Record<string, string>;
  min?: Record<string, string>;
  max?: Record<string, string>;
  maxLimitRequestRatio?: Record<string, string>;
}
```

**API functions (add to `api.ts`):**

```typescript
export async function fetchLimitsStatus(): Promise<{ available: boolean }>
export async function fetchLimitsSummary(): Promise<NamespaceSummary[]>
export async function fetchNamespaceLimits(namespace: string): Promise<NamespaceLimits>
```

### Step 4: Dashboard Island with Slide-Out Panel

**Files:**
- `frontend/islands/NamespaceLimitsDashboard.tsx`
- `frontend/routes/platform/namespace-limits.tsx`
- `frontend/components/nav/SubNav.tsx` (modify)
- `frontend/lib/command-palette-actions.ts` (modify)

**Dashboard island features:**
- Summary cards: Total with Quotas, Warning count, Critical count, No Quota count
- Filterable/sortable table with columns:
  - Namespace (clickable → opens slide-out panel)
  - CPU bar (used/hard)
  - Memory bar (used/hard)
  - Highest % (default sort desc)
  - Status badge
  - Actions (Edit, Delete)
- Filter dropdown: All / Warning / Critical / No Quota
- "Create Namespace Limits" button → wizard
- Auto-refresh via usePoll (30s interval)
- **Slide-out panel** for namespace detail (replaces separate route):
  - Header with namespace name, status badge, Edit/Delete
  - Quotas section with utilization bars and threshold markers
  - LimitRanges section with defaults table
  - Close button or click-outside to dismiss

**Route (`namespace-limits.tsx`):**

```typescript
import { define } from "@/utils.ts";
import NamespaceLimitsDashboard from "@/islands/NamespaceLimitsDashboard.tsx";

export default define.page(function NamespaceLimitsPage() {
  return <NamespaceLimitsDashboard />;
});
```

**SubNav:**
- Add "Namespace Limits" tab to Platform section
- Position after Namespaces, before Storage

**Command palette:**
- Add "Go to Namespace Limits" action
- Add "Create Namespace Limits" action (opens wizard)

---

## Phase 4: Wizard

### Step 5: Wizard Island (4 Steps)

**Files:**
- `frontend/islands/NamespaceLimitsWizard.tsx`
- `frontend/components/wizards/namespace-limits/NamespacePresetStep.tsx`
- `frontend/components/wizards/namespace-limits/QuotaValuesStep.tsx`
- `frontend/components/wizards/namespace-limits/LimitRangeValuesStep.tsx`
- `frontend/components/wizards/namespace-limits/ReviewStep.tsx`

**Wizard steps (collapsed from 6 to 4):**

| Step | Component | Content |
|------|-----------|---------|
| 1 | `NamespacePresetStep.tsx` | Namespace selector + preset (Small/Standard/Large/Custom) |
| 2 | `QuotaValuesStep.tsx` | CPU/memory/pods sliders. "Show advanced" toggle reveals count limits, scope selectors, GPU, threshold overrides |
| 3 | `LimitRangeValuesStep.tsx` | Container defaults (default/defaultRequest/min/max). "Show advanced" toggle reveals Pod limits, PVC limits |
| 4 | `ReviewStep.tsx` | YAML preview (both objects), summary, Apply button |

**Preset values:**

| Preset | CPU | Memory | Pods | Container Default | Container Max |
|--------|-----|--------|------|-------------------|---------------|
| Small | 2 | 4Gi | 10 | 100m/128Mi | 1/2Gi |
| Standard | 8 | 16Gi | 20 | 250m/256Mi | 2/4Gi |
| Large | 32 | 64Gi | 100 | 500m/512Mi | 4/8Gi |

**Wizard reuses existing patterns:**
- `WizardStepper` shell from `components/wizards/WizardStepper.tsx`
- YAML preview via Monaco editor
- Server-side apply via `POST /yaml/apply`

### Step 6: Backend Wizard Support

**Files:**
- `backend/internal/wizard/namespace_limits.go`
- `backend/internal/wizard/namespace_limits_test.go`
- `backend/internal/wizard/handler.go` (modify)

**Input type (nested structs):**

```go
type NamespaceLimitsInput struct {
    Namespace string      `json:"namespace"`
    Quota     QuotaConfig `json:"quota"`
    Limits    LimitConfig `json:"limits"`
}

type QuotaConfig struct {
    // Required
    CPUHard    string `json:"cpuHard"`
    MemoryHard string `json:"memoryHard"`
    PodsHard   int    `json:"podsHard"`
    
    // Advanced (optional)
    SecretsHard       int     `json:"secretsHard,omitempty"`
    ConfigMapsHard    int     `json:"configMapsHard,omitempty"`
    ServicesHard      int     `json:"servicesHard,omitempty"`
    PVCsHard          int     `json:"pvcsHard,omitempty"`
    GPUHard           string  `json:"gpuHard,omitempty"`
    WarnThreshold     float64 `json:"warnThreshold,omitempty"`
    CriticalThreshold float64 `json:"criticalThreshold,omitempty"`
}

type LimitConfig struct {
    // Container limits (required)
    ContainerDefault    ResourcePair `json:"containerDefault"`
    ContainerDefaultReq ResourcePair `json:"containerDefaultRequest"`
    ContainerMax        ResourcePair `json:"containerMax"`
    ContainerMin        ResourcePair `json:"containerMin"`
    
    // Advanced (optional)
    PodMax       ResourcePair `json:"podMax,omitempty"`
    PVCMinStorage string       `json:"pvcMinStorage,omitempty"`
    PVCMaxStorage string       `json:"pvcMaxStorage,omitempty"`
}

type ResourcePair struct {
    CPU    string `json:"cpu"`
    Memory string `json:"memory"`
}
```

**Handler registration:**
- Add `"namespace-limits"` to wizard type switch in `handler.go`

---

## Phase 5: Testing

### Step 7: E2E Tests and Documentation

**Files:**
- `e2e/namespace-limits.spec.ts`
- `CLAUDE.md` (update Build Progress)

**E2E test cases:**
- Navigate to dashboard, verify table renders
- Filter by status (Warning, Critical)
- Create namespace limits via wizard with Standard preset
- Verify new entry appears in dashboard
- Click namespace row, verify slide-out panel opens with utilization bars
- Edit thresholds via wizard, verify update
- Delete namespace limits, verify removal
- RBAC: non-admin user sees filtered results
- Multi-cluster: X-Cluster-ID header routing works

**CLAUDE.md update:**
- Check off "Resource Quota & LimitRange Management" in roadmap
- Add to Post-Phase Enhancements list with PR number

---

## File Inventory

### New Files (12)

| Path | Purpose |
|------|---------|
| `backend/internal/limits/types.go` | Type definitions + interfaces |
| `backend/internal/limits/handler.go` | HTTP handlers + business logic |
| `backend/internal/limits/handler_test.go` | Handler unit tests |
| `backend/internal/limits/checker.go` | Background poller |
| `backend/internal/limits/checker_test.go` | Checker tests |
| `frontend/lib/limits-types.ts` | TypeScript types |
| `frontend/islands/NamespaceLimitsDashboard.tsx` | Dashboard + slide-out panel |
| `frontend/islands/NamespaceLimitsWizard.tsx` | Wizard island |
| `frontend/routes/platform/namespace-limits.tsx` | Dashboard route |
| `frontend/components/wizards/namespace-limits/*.tsx` | 4 wizard step components |
| `backend/internal/wizard/namespace_limits.go` | Wizard input → YAML |
| `backend/internal/wizard/namespace_limits_test.go` | Wizard tests |
| `e2e/namespace-limits.spec.ts` | E2E tests |

### Modified Files (6)

| Path | Change |
|------|--------|
| `backend/internal/server/routes.go` | Add `/limits` routes |
| `backend/internal/server/server.go` | Wire LimitsHandler, start/stop checker |
| `backend/internal/wizard/handler.go` | Add namespace-limits type |
| `frontend/lib/api.ts` | Add limits API functions |
| `frontend/components/nav/SubNav.tsx` | Add Namespace Limits tab |
| `frontend/lib/command-palette-actions.ts` | Add limits actions |

---

## Implementation Order

```
Phase 1 (Backend Foundation):
  Step 1: Types + Handler + Tests          ← foundation

Phase 2 (Background Checker):
  Step 2: Checker + Notification event     ← depends on Step 1

Phase 3 (Frontend):
  Step 3: Types + API client               ← depends on Step 1
  Step 4: Dashboard + slide-out + nav      ← depends on Step 3

Phase 4 (Wizard):
  Step 5: Wizard island (4 steps)          ← depends on Step 3
  Step 6: Backend wizard support           ← parallel with Step 5

Phase 5 (Testing):
  Step 7: E2E tests + documentation        ← depends on all above
```

7 steps (down from 12). Estimated: 3-4 PRs.

---

## Acceptance Criteria

### Functional
- [ ] Admin can view all namespaces with quota status on dashboard
- [ ] Dashboard shows CPU/memory utilization bars with correct percentages
- [ ] Dashboard filters by status (All/Warning/Critical/No Quota)
- [ ] Slide-out panel shows full quota and limit range breakdown
- [ ] Utilization bars display threshold markers at configured percentages
- [ ] Wizard creates both ResourceQuota and LimitRange in one flow
- [ ] Wizard presets populate correct values
- [ ] Wizard "Show advanced" toggles reveal additional options
- [ ] Background checker runs every 5 minutes
- [ ] Notifications dispatch when thresholds are crossed
- [ ] Per-quota annotation overrides respected for thresholds
- [ ] Multi-cluster: dashboard/detail work via ClusterRouter

### Non-Functional
- [ ] Dashboard loads < 500ms for 100 namespaces
- [ ] RBAC: non-admin users see only permitted namespaces
- [ ] No new database tables (annotations-based thresholds)
- [ ] Checker gracefully handles errors without crashing
- [ ] Singleflight prevents thundering herd on dashboard endpoint

### Quality Gates
- [ ] Unit tests for handler, checker, wizard (including RBAC and error paths)
- [ ] E2E tests for create/view/edit/delete flow
- [ ] `go vet` + `deno lint` pass
- [ ] TypeScript strict mode, no `any` types

---

## Review Changes Applied

Changes from code review (2026-04-10):

| Finding | Resolution |
|---------|------------|
| Package name `quota` → `limits` | Renamed package and routes |
| Raw `corev1.LimitRange` → normalized | Added `NormalizedLimitRange`, `NormalizedQuota` types |
| Missing singleflight+cache | Added to Handler struct |
| Missing Logger | Added `Logger *slog.Logger` to Handler |
| Route prefix `/quota` → `/limits` | Updated to `/limits/status`, `/limits/namespaces`, `/limits/namespaces/{ns}` |
| Service/Handler split | Merged into Handler per codebase patterns |
| Wizard 6 steps → 4 steps | Collapsed advanced into main steps via toggle |
| Detail page → slide-out panel | Removed separate route, added slide-out to dashboard |
| Step 4 merged into Step 3 | Notification event registration now in Step 2 |
| Composite key `/` → `:` | State key uses colon delimiter |
| Flat wizard input → nested | Added `QuotaConfig`, `LimitConfig`, `ResourcePair` structs |
| Missing RBAC tests | Added to test list |
| 12 steps → 7 steps | Consolidated phases |
