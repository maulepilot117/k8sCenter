# Namespace Limits Management

Feature: Unified ResourceQuota + LimitRange management with utilization dashboard, tiered wizard, and overage notifications.

**Priority:** #4 on roadmap (after Notification Center, Git commit display, Diff view)
**Design Date:** 2026-04-10

---

## Overview

Admin-first feature for managing namespace resource limits. Combines ResourceQuota (aggregate caps) and LimitRange (per-object defaults/bounds) into a unified "Namespace Limits" surface. Includes:

- Dashboard showing all namespaces with quota posture and utilization
- Detail page per namespace with breakdown of quotas and limit ranges
- Tiered wizard (presets + advanced options) for creating both objects in one flow
- Background checker with Notification Center integration for overage warnings
- Threshold system: 80% warning, 95% critical (global defaults with per-quota annotation overrides)

---

## Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────────┐
│  Informers      │────▶│  quota.Service   │────▶│  quota.Handler      │
│  (RQ + LR)      │     │  - aggregation   │     │  - HTTP endpoints   │
└─────────────────┘     │  - utilization   │     └─────────────────────┘
                        │  - thresholds    │
┌─────────────────┐     └────────┬─────────┘
│  Background     │              │
│  Checker (5min) │──────────────┘
└────────┬────────┘              │
         │                       ▼
         │              ┌─────────────────────┐
         └─────────────▶│  Notification       │
                        │  Center (dispatch)  │
                        └─────────────────────┘
```

**Key decisions:**
- No new database tables — quotas/limitranges are Kubernetes-native, thresholds stored as annotations
- Background checker monitors local cluster only (5-minute interval)
- Multi-cluster support via ClusterRouter for dashboard/detail pages

---

## Phase 1: Backend Foundation

### Step 1: Types and Service

**Files:**
- `backend/internal/quota/types.go`
- `backend/internal/quota/service.go`
- `backend/internal/quota/service_test.go`

**Types (`types.go`):**

```go
package quota

import (
    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/api/resource"
)

type ThresholdStatus string

const (
    ThresholdOK       ThresholdStatus = "ok"
    ThresholdWarning  ThresholdStatus = "warning"
    ThresholdCritical ThresholdStatus = "critical"
)

// NamespaceSummary is the dashboard row for one namespace
type NamespaceSummary struct {
    Namespace          string          `json:"namespace"`
    HasQuota           bool            `json:"hasQuota"`
    HasLimitRange      bool            `json:"hasLimitRange"`
    CPUUsedPercent     float64         `json:"cpuUsedPercent"`
    MemoryUsedPercent  float64         `json:"memoryUsedPercent"`
    HighestUtilization float64         `json:"highestUtilization"`
    Status             ThresholdStatus `json:"status"`
    QuotaCount         int             `json:"quotaCount"`
    LimitRangeCount    int             `json:"limitRangeCount"`
}

// NamespaceLimits is the detail view for one namespace
type NamespaceLimits struct {
    Namespace   string                 `json:"namespace"`
    Quotas      []QuotaWithUtilization `json:"quotas"`
    LimitRanges []corev1.LimitRange    `json:"limitRanges"`
}

// QuotaWithUtilization wraps a ResourceQuota with computed utilization
type QuotaWithUtilization struct {
    Name              string                              `json:"name"`
    Utilization       map[corev1.ResourceName]ResourceUtilization `json:"utilization"`
    WarnThreshold     float64                             `json:"warnThreshold"`
    CriticalThreshold float64                             `json:"criticalThreshold"`
    Quota             corev1.ResourceQuota                `json:"-"` // internal use
}

// ResourceUtilization tracks usage for one resource dimension
type ResourceUtilization struct {
    Used       resource.Quantity `json:"used"`
    Hard       resource.Quantity `json:"hard"`
    Percentage float64           `json:"percentage"`
    Status     ThresholdStatus   `json:"status"`
}
```

**Service (`service.go`):**

```go
package quota

import (
    "context"
    
    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/labels"
)

const (
    DefaultWarnThreshold     = 0.80
    DefaultCriticalThreshold = 0.95
    
    AnnotationWarnThreshold     = "k8scenter.io/warn-threshold"
    AnnotationCriticalThreshold = "k8scenter.io/critical-threshold"
)

type Service struct {
    informers InformerSource // interface for ResourceQuota + LimitRange listers
}

func NewService(informers InformerSource) *Service

// ListNamespaceLimitsSummary returns dashboard data for all namespaces
func (s *Service) ListNamespaceLimitsSummary(ctx context.Context, userNamespaces []string) ([]NamespaceSummary, error)

// GetNamespaceLimits returns detailed limits for one namespace
func (s *Service) GetNamespaceLimits(ctx context.Context, namespace string) (*NamespaceLimits, error)

// CheckThresholds evaluates quota against thresholds, returns worst status
func (s *Service) CheckThresholds(quota *corev1.ResourceQuota) ThresholdStatus

// ParseThresholdAnnotations extracts thresholds from annotations with defaults
func ParseThresholdAnnotations(quota *corev1.ResourceQuota) (warn, critical float64)

// computeUtilization calculates percentage and status for each resource
func (s *Service) computeUtilization(quota *corev1.ResourceQuota) map[corev1.ResourceName]ResourceUtilization
```

**Tests (`service_test.go`):**
- `TestCheckThresholds` — 80/95 defaults, annotation overrides, edge cases
- `TestParseThresholdAnnotations` — valid, invalid, missing annotations
- `TestComputeUtilization` — 0%, 50%, 100%, over 100%
- `TestListNamespaceLimitsSummary` — multiple namespaces, RBAC filtering

### Step 2: HTTP Handler and Routes

**Files:**
- `backend/internal/quota/handler.go`
- `backend/internal/server/routes.go` (modify)

**Handler (`handler.go`):**

```go
package quota

import (
    "net/http"
    
    "github.com/go-chi/chi/v5"
)

type Handler struct {
    Service      *Service
    AccessChecker AccessChecker
}

func NewHandler(service *Service, accessChecker AccessChecker) *Handler

// HandleListSummary handles GET /quota/summary
func (h *Handler) HandleListSummary(w http.ResponseWriter, r *http.Request)

// HandleGetNamespaceLimits handles GET /quota/{namespace}
func (h *Handler) HandleGetNamespaceLimits(w http.ResponseWriter, r *http.Request)
```

**Routes (add to `routes.go`):**

```go
// In registerAuthenticatedRoutes or similar
r.Route("/quota", func(qr chi.Router) {
    qr.Get("/summary", s.QuotaHandler.HandleListSummary)
    qr.Get("/{namespace}", s.QuotaHandler.HandleGetNamespaceLimits)
})
```

**Server wiring (`server.go`):**
- Add `QuotaHandler *quota.Handler` to Server struct
- Initialize in NewServer with informers and access checker

---

## Phase 2: Background Checker and Notifications

### Step 3: Background Checker

**Files:**
- `backend/internal/quota/checker.go`
- `backend/internal/quota/checker_test.go`

**Checker (`checker.go`):**

```go
package quota

import (
    "context"
    "sync"
    "time"
    
    "github.com/maulepilot117/k8scenter/backend/internal/notifications"
)

const (
    DefaultCheckInterval = 5 * time.Minute
    EventTypeThreshold   = "quota.threshold_crossed"
)

type Checker struct {
    service  *Service
    notifier *notifications.Service
    interval time.Duration
    
    mu        sync.Mutex
    lastState map[string]ThresholdStatus // key: "namespace/quotaName/resource"
}

func NewChecker(service *Service, notifier *notifications.Service, interval time.Duration) *Checker

// Start begins the background checking loop
func (c *Checker) Start(ctx context.Context)

// check runs one check cycle across all quotas
func (c *Checker) check(ctx context.Context)

// dispatchIfChanged sends notification only when status changes
func (c *Checker) dispatchIfChanged(key string, current ThresholdStatus, event QuotaThresholdEvent)
```

**Event payload:**

```go
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
```

**Server integration (`server.go`):**
- Start checker goroutine in `Run()` method
- Pass server context for graceful shutdown

### Step 4: Notification Event Type Registration

**Files:**
- `backend/internal/notifications/events.go` (modify or create)
- `backend/internal/store/notifications.go` (seed default rule)

**Register event type:**

```go
// Add to event type registry
const EventTypeQuotaThreshold = "quota.threshold_crossed"

// Event metadata for UI
var QuotaThresholdEventMeta = EventTypeMeta{
    Type:        EventTypeQuotaThreshold,
    DisplayName: "Quota Threshold Crossed",
    Description: "Namespace approaching or exceeding resource quota",
    Category:    "platform",
}
```

**Seed default rule (optional):**
- Create rule: `quota.threshold_crossed` where `status == "critical"` → in-app feed enabled

---

## Phase 3: Frontend Dashboard and Detail

### Step 5: TypeScript Types and API Client

**Files:**
- `frontend/lib/quota-types.ts`
- `frontend/lib/api.ts` (modify)

**Types (`quota-types.ts`):**

```typescript
export type ThresholdStatus = "ok" | "warning" | "critical";

export interface NamespaceSummary {
  namespace: string;
  hasQuota: boolean;
  hasLimitRange: boolean;
  cpuUsedPercent: number;
  memoryUsedPercent: number;
  highestUtilization: number;
  status: ThresholdStatus;
  quotaCount: number;
  limitRangeCount: number;
}

export interface NamespaceLimits {
  namespace: string;
  quotas: QuotaWithUtilization[];
  limitRanges: LimitRangeInfo[];
}

export interface QuotaWithUtilization {
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

export interface LimitRangeInfo {
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
export async function fetchQuotaSummary(): Promise<NamespaceSummary[]>
export async function fetchNamespaceLimits(namespace: string): Promise<NamespaceLimits>
```

### Step 6: Dashboard Island

**Files:**
- `frontend/islands/NamespaceLimitsDashboard.tsx`
- `frontend/routes/platform/namespace-limits.tsx`

**Dashboard island features:**
- Summary cards: Total with Quotas, Warning count, Critical count, No Quota count
- Filterable table with columns:
  - Namespace (link to detail)
  - CPU bar (used/hard)
  - Memory bar (used/hard)
  - Highest % (default sort desc)
  - Status badge
  - Actions (Edit, Delete)
- Filter dropdown: All / Warning / Critical / No Quota
- "Create Namespace Limits" button → wizard
- Auto-refresh via usePoll (30s interval)

**Route (`namespace-limits.tsx`):**

```typescript
import { define } from "@/utils.ts";
import NamespaceLimitsDashboard from "@/islands/NamespaceLimitsDashboard.tsx";

export default define.page(function NamespaceLimitsPage() {
  return <NamespaceLimitsDashboard />;
});
```

### Step 7: Detail Page Island

**Files:**
- `frontend/islands/NamespaceLimitsDetail.tsx`
- `frontend/routes/platform/namespace-limits/[namespace].tsx`

**Detail island features:**
- Header: namespace name, overall status badge, Edit/Delete buttons
- Quotas section: card per quota with utilization bars per resource
  - Bars show threshold markers (80%, 95% lines)
  - Current values displayed (e.g., "4 / 8 CPU")
- LimitRanges section: card per limit range
  - Table: Type, Default, Default Request, Min, Max
- Related links: namespace page, pods in namespace

### Step 8: Navigation Updates

**Files:**
- `frontend/components/nav/SubNav.tsx` (modify)
- `frontend/lib/command-palette-actions.ts` (modify)

**SubNav:**
- Add "Namespace Limits" tab to Platform section
- Position after Namespaces, before Storage

**Command palette:**
- Add "Go to Namespace Limits" action
- Add "Create Namespace Limits" action (opens wizard)

---

## Phase 4: Wizard

### Step 9: Wizard Island

**Files:**
- `frontend/islands/NamespaceLimitsWizard.tsx`
- `frontend/components/wizards/namespace-limits/` (step components)

**Wizard steps:**

| Step | Component | Content |
|------|-----------|---------|
| 1 | `NamespaceStep.tsx` | Namespace selector (dropdown), "Add to existing" toggle |
| 2 | `QuotaTemplateStep.tsx` | Presets (Standard/Small/Large/Custom), CPU/memory/pods sliders |
| 3 | `QuotaAdvancedStep.tsx` | Collapsed. Count limits, scope selectors, GPU, threshold overrides |
| 4 | `LimitRangeStep.tsx` | Container defaults: default/defaultRequest/min/max with presets |
| 5 | `LimitRangeAdvancedStep.tsx` | Collapsed. Pod limits, PVC limits, maxLimitRequestRatio |
| 6 | `ReviewStep.tsx` | YAML preview (both objects), summary, Apply button |

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

### Step 10: Backend Wizard Support

**Files:**
- `backend/internal/wizard/namespace_limits.go`
- `backend/internal/wizard/namespace_limits_test.go`
- `backend/internal/wizard/handler.go` (modify)

**Input type:**

```go
type NamespaceLimitsInput struct {
    Namespace string `json:"namespace"`
    
    // Quota
    CPUHard      string `json:"cpuHard"`
    MemoryHard   string `json:"memoryHard"`
    PodsHard     int    `json:"podsHard"`
    
    // Advanced quota (optional)
    SecretsHard     *int    `json:"secretsHard,omitempty"`
    ConfigMapsHard  *int    `json:"configMapsHard,omitempty"`
    ServicesHard    *int    `json:"servicesHard,omitempty"`
    PVCsHard        *int    `json:"pvcsHard,omitempty"`
    GPUHard         *string `json:"gpuHard,omitempty"`
    WarnThreshold   *float64 `json:"warnThreshold,omitempty"`
    CriticalThreshold *float64 `json:"criticalThreshold,omitempty"`
    
    // LimitRange
    ContainerDefaultCPU      string `json:"containerDefaultCpu"`
    ContainerDefaultMemory   string `json:"containerDefaultMemory"`
    ContainerDefaultReqCPU   string `json:"containerDefaultReqCpu"`
    ContainerDefaultReqMem   string `json:"containerDefaultReqMemory"`
    ContainerMaxCPU          string `json:"containerMaxCpu"`
    ContainerMaxMemory       string `json:"containerMaxMemory"`
    ContainerMinCPU          string `json:"containerMinCpu"`
    ContainerMinMemory       string `json:"containerMinMemory"`
    
    // Advanced limit range (optional)
    PodMaxCPU    *string `json:"podMaxCpu,omitempty"`
    PodMaxMemory *string `json:"podMaxMemory,omitempty"`
    PVCMinStorage *string `json:"pvcMinStorage,omitempty"`
    PVCMaxStorage *string `json:"pvcMaxStorage,omitempty"`
}
```

**Handler registration:**
- Add `"namespace-limits"` to wizard type switch in `handler.go`

---

## Phase 5: Polish and Testing

### Step 11: E2E Tests

**Files:**
- `e2e/namespace-limits.spec.ts`

**Test cases:**
- Navigate to dashboard, verify table renders
- Create namespace limits via wizard with Standard preset
- Verify new entry appears in dashboard
- Navigate to detail page, verify utilization bars
- Edit thresholds, verify update
- Delete namespace limits, verify removal

### Step 12: Documentation

**Files:**
- `CLAUDE.md` (update Build Progress)

**Update:**
- Check off "Resource Quota & LimitRange Management" in roadmap
- Add to Post-Phase Enhancements list with PR number

---

## File Inventory

### New Files (15)

| Path | Purpose |
|------|---------|
| `backend/internal/quota/types.go` | Type definitions |
| `backend/internal/quota/service.go` | Business logic |
| `backend/internal/quota/service_test.go` | Unit tests |
| `backend/internal/quota/handler.go` | HTTP handlers |
| `backend/internal/quota/checker.go` | Background poller |
| `backend/internal/quota/checker_test.go` | Checker tests |
| `frontend/lib/quota-types.ts` | TypeScript types |
| `frontend/islands/NamespaceLimitsDashboard.tsx` | Dashboard island |
| `frontend/islands/NamespaceLimitsDetail.tsx` | Detail island |
| `frontend/islands/NamespaceLimitsWizard.tsx` | Wizard island |
| `frontend/routes/platform/namespace-limits.tsx` | Dashboard route |
| `frontend/routes/platform/namespace-limits/[namespace].tsx` | Detail route |
| `frontend/components/wizards/namespace-limits/*.tsx` | Wizard step components (6 files) |
| `backend/internal/wizard/namespace_limits.go` | Wizard input → YAML |
| `backend/internal/wizard/namespace_limits_test.go` | Wizard tests |
| `e2e/namespace-limits.spec.ts` | E2E tests |

### Modified Files (7)

| Path | Change |
|------|--------|
| `backend/internal/server/routes.go` | Add `/quota` routes |
| `backend/internal/server/server.go` | Wire QuotaHandler, start checker |
| `backend/internal/wizard/handler.go` | Add namespace-limits type |
| `backend/internal/notifications/events.go` | Register quota event type |
| `frontend/lib/api.ts` | Add quota API functions |
| `frontend/components/nav/SubNav.tsx` | Add Namespace Limits tab |
| `frontend/lib/command-palette-actions.ts` | Add quota actions |

---

## Implementation Order

```
Phase 1 (Backend Foundation):
  Step 1: Types + Service + Tests     ← foundation
  Step 2: Handler + Routes            ← depends on Step 1

Phase 2 (Background Checker):
  Step 3: Checker goroutine           ← depends on Step 1
  Step 4: Notification integration    ← depends on Step 3

Phase 3 (Frontend Dashboard/Detail):
  Step 5: Types + API client          ← depends on Step 2
  Step 6: Dashboard island            ← depends on Step 5
  Step 7: Detail island               ← parallel with Step 6
  Step 8: Navigation updates          ← depends on Steps 6, 7

Phase 4 (Wizard):
  Step 9: Wizard island               ← depends on Step 5
  Step 10: Backend wizard support     ← parallel with Step 9

Phase 5 (Polish):
  Step 11: E2E tests                  ← depends on all above
  Step 12: Documentation              ← final step
```

---

## Acceptance Criteria

### Functional
- [ ] Admin can view all namespaces with quota status on dashboard
- [ ] Dashboard shows CPU/memory utilization bars with correct percentages
- [ ] Dashboard filters by status (All/Warning/Critical/No Quota)
- [ ] Detail page shows full quota and limit range breakdown
- [ ] Utilization bars display threshold markers at configured percentages
- [ ] Wizard creates both ResourceQuota and LimitRange in one flow
- [ ] Wizard presets populate correct values
- [ ] Wizard advanced sections reveal additional options
- [ ] Background checker runs every 5 minutes
- [ ] Notifications dispatch when thresholds are crossed
- [ ] Per-quota annotation overrides respected for thresholds
- [ ] Multi-cluster: dashboard/detail work via ClusterRouter

### Non-Functional
- [ ] Dashboard loads < 500ms for 100 namespaces
- [ ] RBAC: non-admin users see only permitted namespaces
- [ ] No new database tables (annotations-based thresholds)
- [ ] Checker gracefully handles errors without crashing

### Quality Gates
- [ ] Unit tests for service, checker, wizard
- [ ] E2E tests for create/view/edit/delete flow
- [ ] `go vet` + `deno lint` pass
- [ ] TypeScript strict mode, no `any` types
