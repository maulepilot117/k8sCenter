# Phase 8: Policy & Governance — Implementation Plan (v2)

## Overview

Phase 8 adds policy engine integration (Kyverno + OPA/Gatekeeper), compliance dashboards, and violation browsing. Auto-detects which engine(s) are deployed, normalizes data across both, and provides a unified governance experience. Two sub-phases: 8A (backend) + 8B (frontend).

**Changes from v1 (reviewer feedback):**
- **Merged Phase 8B (compliance) into 8A** — scoring is ~50 LOC, not a separate phase
- **Deleted WebhookDashboard** — duplicates existing admin webhook pages
- **Deleted HandleGetPolicy** — no frontend route consumes it, GenericCRDHandler provides raw access
- **Added singleflight + 30s cache + bounded concurrency** for Gatekeeper constraint enumeration
- **Added namespace-level RBAC filtering** on violations via AccessChecker
- **Extended AccessChecker.CanAccess** to accept API group parameter (backward-compatible)
- **Handler uses ClusterRouter** (not K8sClient) for impersonation, matching crd_handler.go
- **Added `Blocking bool`** to NormalizedPolicy/NormalizedViolation for enforcement semantics
- **Default unspecified severity = "medium"**, dropped ByCategory/CategoryCounts, dropped Version field
- **Reuse CRDDiscovery.ListCRDs()** filtered by group for Gatekeeper — no duplicate discovery
- **Multi-cluster: discovery = local only; reads = ClusterRouter per-request** (documented)

---

## Phase 8A: Full Backend — Discovery, Adapters, Compliance, Handler

**Branch:** `feat/phase8a-policy-backend`

### Step 8A.1 — Extend AccessChecker with API Group Support

**Prerequisite:** `AccessChecker.CanAccess` currently takes `(ctx, username, groups, verb, resource, namespace)`. Policy CRDs need group-specific RBAC checks (e.g., `list clusterpolicies` in group `kyverno.io`). Extend the signature.

**File:** `backend/internal/k8s/resources/access.go`

Add a new method (backward-compatible, existing callers unchanged):
```go
// CanAccessGroupResource checks RBAC for a specific API group + resource.
// For core API resources, pass group="" (existing CanAccess behavior).
func (ac *AccessChecker) CanAccessGroupResource(ctx context.Context, username string, groups []string, verb, apiGroup, resource, namespace string) (bool, error)
```

The existing `CanAccess` delegates to `CanAccessGroupResource` with `apiGroup=""`.

Internally, the `SelfSubjectAccessReview` already supports `Group` in `ResourceAttributes` — just populate it.

**Files to modify:**
- `backend/internal/k8s/resources/access.go` (add method, refactor existing)
- `backend/internal/k8s/resources/access_test.go` (add test for group parameter)

### Step 8A.2 — Policy Discovery

**New package:** `backend/internal/policy/`

**`backend/internal/policy/discovery.go`**

Follow monitoring/loki discoverer pattern:

- `PolicyDiscoverer` struct with `sync.RWMutex`, cached `*EngineStatus`
- Constructor: `NewDiscoverer(k8sClient *k8s.ClientFactory, crdDiscovery *k8s.CRDDiscovery, logger *slog.Logger)`
- `RunDiscoveryLoop(ctx)` — immediate + 5-min ticker
- `Discover(ctx)`:
  1. CRD check via `k8sClient.DiscoveryClient().ServerResourcesForGroupVersion("kyverno.io/v1")` → ClusterPolicy kind
  2. CRD check via `k8sClient.DiscoveryClient().ServerResourcesForGroupVersion("templates.gatekeeper.sh/v1")` → ConstraintTemplate kind
  3. Service health: verify pods Running in `kyverno` / `gatekeeper-system` namespaces (service account scope — OK for background discovery)
  4. For Gatekeeper: use `crdDiscovery.ListCRDs()` filtered to `constraints.gatekeeper.sh` group to enumerate constraint CRDs
- States: `none`, `kyverno`, `gatekeeper`, `both`
- `Status()` accessor: returns `EngineStatus` (admin sees full details, non-admin sees only engine type)
- **Multi-cluster note:** Discovery runs against local cluster only. Remote cluster policy reads go through ClusterRouter per-request.

### Step 8A.3 — Unified Types

**`backend/internal/policy/types.go`**

```go
type Engine string
const (
    EngineNone       Engine = "none"
    EngineKyverno    Engine = "kyverno"
    EngineGatekeeper Engine = "gatekeeper"
    EngineBoth       Engine = "both"
)

type EngineStatus struct {
    Detected    Engine        `json:"detected"`
    Kyverno     *EngineDetail `json:"kyverno,omitempty"`
    Gatekeeper  *EngineDetail `json:"gatekeeper,omitempty"`
    LastChecked string        `json:"lastChecked"`
}

type EngineDetail struct {
    Available bool   `json:"available"`
    Namespace string `json:"namespace"`
    Webhooks  int    `json:"webhooks"`
}
// No Version field — detection strategy not reliable enough.

type NormalizedPolicy struct {
    ID              string   `json:"id"`         // composite: "{engine}:{namespace}/{name}" or "{engine}::{name}" for cluster-scoped
    Name            string   `json:"name"`
    Namespace       string   `json:"namespace,omitempty"`
    Engine          Engine   `json:"engine"`
    Kind            string   `json:"kind"`       // ClusterPolicy, Policy, K8sRequiredLabels, etc.
    Action          string   `json:"action"`     // enforce, audit, deny, dryrun, warn
    Blocking        bool     `json:"blocking"`   // true if policy actually blocks admission
    Category        string   `json:"category"`
    Severity        string   `json:"severity"`   // critical, high, medium, low (default: medium)
    Description     string   `json:"description"`
    Ready           bool     `json:"ready"`
    RuleCount       int      `json:"ruleCount"`
    ViolationCount  int      `json:"violationCount"`
    TargetKinds     []string `json:"targetKinds"`
    NativeAction    string   `json:"nativeAction"` // original engine-specific action string
}

type NormalizedViolation struct {
    Policy    string `json:"policy"`
    Rule      string `json:"rule,omitempty"`
    Engine    Engine `json:"engine"`
    Severity  string `json:"severity"`
    Action    string `json:"action"`    // denied, warned, audited
    Blocking  bool   `json:"blocking"`  // was the resource actually blocked?
    Message   string `json:"message"`
    Namespace string `json:"namespace"`
    Kind      string `json:"kind"`
    Name      string `json:"name"`
    Timestamp string `json:"timestamp,omitempty"`
}

type ComplianceScore struct {
    Scope      string                    `json:"scope"` // "cluster" or namespace name
    Score      float64                   `json:"score"` // 0-100
    Pass       int                       `json:"pass"`
    Fail       int                       `json:"fail"`
    Warn       int                       `json:"warn"`
    Total      int                       `json:"total"`
    BySeverity map[string]SeverityCounts `json:"bySeverity"`
}
// No ByCategory — categories from annotations are unreliable.

type SeverityCounts struct {
    Pass  int `json:"pass"`
    Fail  int `json:"fail"`
    Total int `json:"total"`
}
```

Default severity for policies without annotations: `"medium"` (weight 2).

Severity weights: `critical=10, high=5, medium=2, low=1`.

### Step 8A.4 — Kyverno Adapter

**`backend/internal/policy/kyverno.go`**

Functions accept an impersonating `dynamic.Interface` (obtained from ClusterRouter):

- `ListKyvernoPolicies(ctx, dynClient) ([]NormalizedPolicy, error)`:
  - List `clusterpolicies` (GVR: kyverno.io/v1/clusterpolicies) via dynamic client
  - List `policies` (GVR: kyverno.io/v1/policies) in all namespaces
  - Normalize: `spec.validationFailureAction` → action + blocking (`Enforce` = blocking, `Audit` = not blocking)
  - Extract annotations: `policies.kyverno.io/{title,category,severity,description}`
  - Default missing severity to "medium"
  - Composite ID: `kyverno:{namespace}/{name}` or `kyverno::{name}` for cluster-scoped

- `ListKyvernoViolations(ctx, dynClient) ([]NormalizedViolation, error)`:
  - List `policyreports` (GVR: wgpolicyk8s.io/v1alpha2/policyreports) in all namespaces
  - List `clusterpolicyreports` (GVR: wgpolicyk8s.io/v1alpha2/clusterpolicyreports)
  - Filter results where `result == "fail"` or `result == "warn"`
  - Normalize: extract policy, rule, message, resource ref, severity, timestamp

### Step 8A.5 — Gatekeeper Adapter

**`backend/internal/policy/gatekeeper.go`**

- `ListGatekeeperPolicies(ctx, dynClient, constraintCRDs []*k8s.CRDInfo) ([]NormalizedPolicy, error)`:
  - List `constrainttemplates` (GVR: templates.gatekeeper.sh/v1/constrainttemplates)
  - For each constraint CRD from `constraintCRDs` (passed in from discovery — **no duplicate discovery**):
    - List instances via dynamic client with **bounded concurrency** (semaphore, max 5 concurrent)
    - Add `context.WithTimeout(5s)` per constraint type
    - Hard cap: max 100 constraint types enumerated
  - Normalize: `spec.enforcementAction` → action + blocking (`deny` = blocking, `warn`/`dryrun` = not blocking)
  - Extract `metadata.gatekeeper.sh/title`, description from annotations
  - Composite ID: `gatekeeper::{templateKind}/{constraintName}`

- `ListGatekeeperViolations(ctx, dynClient, constraintCRDs []*k8s.CRDInfo) ([]NormalizedViolation, error)`:
  - For each constraint instance, read `status.violations` array
  - Same bounded concurrency (semaphore, max 5)
  - Normalize: kind, name, namespace, message, enforcement action

### Step 8A.6 — Handler with Caching + RBAC

**`backend/internal/policy/handler.go`**

```go
type Handler struct {
    Discoverer    *PolicyDiscoverer
    ClusterRouter *k8s.ClusterRouter       // for impersonating dynamic client
    CRDDiscovery  *k8s.CRDDiscovery        // for Gatekeeper constraint CRD list
    AccessChecker *resources.AccessChecker  // for RBAC checks (with group support)
    Logger        *slog.Logger

    // Response cache: singleflight + 30s TTL
    policyGroup  singleflight.Group
    policyCache  atomic.Value // *cachedPolicies
}

type cachedPolicies struct {
    policies   []NormalizedPolicy
    violations []NormalizedViolation
    fetchedAt  time.Time
}
```

**Cache pattern:**
- `fetchPoliciesAndViolations(ctx, user)` — checks cache (30s TTL). If stale, uses `singleflight.Do` to coalesce concurrent requests. Fetches from both adapters based on discovered engines.
- Cache is per-user? No — cache the raw data globally (it's from informer-like reads), then filter per-user in the handler based on RBAC.

**RBAC filtering on violations (P1 fix):**
- After fetching violations, filter by user's namespace access
- For each unique namespace in violations, call `AccessChecker.CanAccessGroupResource(ctx, user, "list", "", "pods", ns)` (pod access as proxy for namespace visibility)
- Strip violations from namespaces the user can't access
- Compliance scores are also filtered — user only sees scores for accessible namespaces

**impersonatingDynamic helper:**
```go
func (h *Handler) impersonatingDynamic(ctx context.Context, user *auth.User) (dynamic.Interface, error) {
    clusterID := middleware.ClusterIDFromContext(ctx)
    return h.ClusterRouter.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
}
```

**Handlers:**

1. `HandleStatus` — `GET /api/v1/policy/status`
   - Returns `EngineStatus`
   - Non-admin: strip webhook count and namespace details (just engine type)

2. `HandleListPolicies` — `GET /api/v1/policy/policies`
   - Fetch via cached path, return merged list sorted by severity desc
   - RBAC: check `list` on engine CRDs via `CanAccessGroupResource`

3. `HandleListViolations` — `GET /api/v1/policy/violations?namespace=X`
   - Fetch via cached path, **filter by user's namespace RBAC**
   - Optional namespace filter in query param

4. `HandleCompliance` — `GET /api/v1/policy/compliance`
   - Fetch policies + violations (cached), filter violations by RBAC
   - Compute scores: cluster-wide + per-namespace (only accessible namespaces)
   - Inline scoring function (~50 LOC, no separate file)

**Compliance scoring (inlined in handler):**
```go
func computeCompliance(policies []NormalizedPolicy, violations []NormalizedViolation, scope string) ComplianceScore {
    weights := map[string]int{"critical": 10, "high": 5, "medium": 2, "low": 1}
    // ... weighted pass rate calculation
}
```

**Route registration:**
```go
func (s *Server) registerPolicyRoutes(ar chi.Router) {
    h := s.PolicyHandler
    ar.Route("/policy", func(pr chi.Router) {
        rl := s.YAMLRateLimiter
        if rl == nil { rl = s.RateLimiter }
        pr.Use(middleware.RateLimit(rl))
        pr.Get("/status", h.HandleStatus)
        pr.Get("/policies", h.HandleListPolicies)
        pr.Get("/violations", h.HandleListViolations)
        pr.Get("/compliance", h.HandleCompliance)
    })
}
```

**Files to create:**
- `backend/internal/policy/discovery.go`
- `backend/internal/policy/types.go`
- `backend/internal/policy/kyverno.go`
- `backend/internal/policy/gatekeeper.go`
- `backend/internal/policy/handler.go`
- `backend/internal/policy/discovery_test.go`

**Files to modify:**
- `backend/internal/k8s/resources/access.go` (add CanAccessGroupResource)
- `backend/internal/server/server.go` (add PolicyHandler)
- `backend/internal/server/routes.go` (add registerPolicyRoutes)
- `backend/cmd/kubecenter/main.go` (wire policy discoverer + handler)

---

## Phase 8B: Frontend — Policy Dashboard, Violations, Compliance

**Branch:** `feat/phase8b-policy-frontend`

### Step 8B.1 — Navigation Update

**`frontend/lib/constants.ts`**

Add policy tabs to the Security section. Use `/security/` prefix for new routes (existing RBAC stays at `/rbac/` — documented inconsistency, acceptable since both are under the same nav section):

```ts
{
  id: "security",
  tabs: [
    // ... existing RBAC tabs ...
    { label: "Policies", href: "/security/policies" },
    { label: "Violations", href: "/security/violations" },
    { label: "Compliance", href: "/security/compliance" },
  ],
}
```

### Step 8B.2 — Policy List Page

**`frontend/islands/PolicyDashboard.tsx`** (~250 LOC)

Follow ExtensionsHub + RBACOverview patterns:

- Fetch `apiGet("/v1/policy/status")` + `apiGet("/v1/policy/policies")`
- Engine status banner: which engine(s) detected, or "No policy engine" with setup guidance
- Policy table: name, engine badge (Kyverno green / Gatekeeper blue), blocking badge (shield=enforce, eye=audit), severity badge, violation count, target kinds, ready status
- Search/filter by name, engine, severity, blocking/non-blocking
- If no engine detected: show install instructions for Kyverno and Gatekeeper

**`frontend/routes/security/policies.tsx`**

### Step 8B.3 — Violations Page

**`frontend/islands/ViolationBrowser.tsx`** (~200 LOC)

- Fetch `apiGet("/v1/policy/violations?namespace=X")`
- Violation table: policy, severity badge, resource (kind/name → link to detail), namespace, message (truncated), action (denied/warned/audited), blocking badge
- Filters: namespace, severity, engine, policy name
- Click resource → navigate to resource detail page
- Empty state: "No violations found" (with distinction between "no violations" vs "no engine detected")

**`frontend/routes/security/violations.tsx`**

### Step 8B.4 — Compliance Dashboard

**`frontend/islands/ComplianceDashboard.tsx`** (~250 LOC)

- Fetch `apiGet("/v1/policy/compliance")`
- Cluster compliance score ring (reuse existing GaugeRing component): green >80, yellow 50-80, red <50
- Raw counts alongside score: "45/50 passing" (not just "90%")
- Severity breakdown: 4 horizontal bars (critical/high/medium/low) showing pass/fail per severity
- Per-namespace table: namespace, score (color-coded), pass/fail counts, worst violation
- Click namespace → navigate to violations page filtered to that namespace

**`frontend/routes/security/compliance.tsx`**

### Step 8B.5 — Entry Points

- **Dashboard health score:** No change needed — the existing health score is cluster-level, not policy-level
- **Command palette:** Add "View Policies" and "View Violations" quick actions

**Files to create:**
- `frontend/islands/PolicyDashboard.tsx`
- `frontend/islands/ViolationBrowser.tsx`
- `frontend/islands/ComplianceDashboard.tsx`
- `frontend/routes/security/policies.tsx`
- `frontend/routes/security/violations.tsx`
- `frontend/routes/security/compliance.tsx`

**Files to modify:**
- `frontend/lib/constants.ts` (add Policy tabs to Security section)
- `frontend/islands/CommandPalette.tsx` (add policy quick actions)

---

## File Count Summary

| Phase | New Files | Modified Files |
|-------|-----------|---------------|
| 8A (Full Backend) | 6 + 1 test | 4 |
| 8B (Frontend) | 6 | 2 |
| **Total** | **13** | **6** |

Down from 19+5 in v1 (31% reduction in file count).

## New API Surface

| Endpoint | Type |
|----------|------|
| `GET /policy/status` | HTTP |
| `GET /policy/policies` | HTTP |
| `GET /policy/violations` | HTTP |
| `GET /policy/compliance` | HTTP |
| **Total** | **4** |

Down from 6 in v1 (removed webhooks + single policy detail).

## Graceful Degradation

- **No engine:** All endpoints return 200 with empty data + `detected: "none"`. Frontend shows setup instructions.
- **Kyverno only / Gatekeeper only:** Data from detected engine only, other fields empty.
- **Both engines:** Data merged, engine badge indicates source.
- **Engine unhealthy:** Status shows available=false. Cached data from last successful fetch returned.
- **Remote clusters:** Discovery local-only. Reads go through ClusterRouter with impersonation.

## Key Security Decisions

1. **Violations filtered by namespace RBAC** — user only sees violations for namespaces they can access
2. **Compliance scores filtered** — only includes data from accessible namespaces
3. **Status endpoint** — non-admin sees engine type only (no pod/webhook details)
4. **Impersonation for all reads** — ClusterRouter + impersonatingDynamic, never service account
5. **Bounded Gatekeeper fan-out** — semaphore(5), 5s timeout per constraint type, 100 type cap
6. **Default severity "medium"** — prevents NaN scores from missing annotations

## Deferred Work

- Policy creation wizards (17 common policy templates)
- Compliance trend storage (PostgreSQL daily snapshots)
- Policy dry-run / preview
- Per-category compliance scoring (when annotation conventions mature)
- Engine version detection
- Integration with Trivy/Kubescape scan results
