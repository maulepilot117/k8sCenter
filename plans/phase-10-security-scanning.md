# Phase 10: Security Scanning — Implementation Plan (v2)

## Overview

Phase 10 adds security scanner integration (Trivy Operator + Kubescape Operator), providing per-workload vulnerability summaries. Auto-detects which scanner(s) are deployed, normalizes data across both. Single PR: backend + frontend together.

**Architecture:** Follows Phase 8/9 pattern — CRD-based discovery, dual adapter normalization, singleflight+cache handler, per-user RBAC filtering. New package: `internal/scanning/`.

**Changes from v1 (plan review feedback):**
- **Renamed package** `internal/security/` → `internal/scanning/` (too broad, breaks naming convention)
- **Renamed API prefix** `/security-scan/` → `/scanning/` (single-word convention)
- **Deferred compliance** — no frontend consumer, overlaps with Phase 8 policy compliance
- **Deferred config audits** — overlaps with Kyverno/Gatekeeper violations, no dedup strategy
- **Deferred vuln detail page** — users can view raw CRDs via existing resource browser
- **Cut `/summary` endpoint** — frontend computes totals client-side from vuln list
- **Single PR** instead of two phases — no architectural novelty justifying a split
- **Namespace-scoped caching** — global 30s cache is insufficient for 6000+ VulnerabilityReports; fetch per-namespace with singleflight keyed by namespace
- **Frontend routes** flat under `/security/vulnerabilities` (no `/scanning/` nesting — no collision with `/security/violations`)

---

## Backend — Discovery, Adapters, Handler

**Branch:** `feat/phase10-security-scanning`

### Step 1 — Types

**New file:** `backend/internal/scanning/types.go`

```go
type Scanner string
const (
    ScannerNone      Scanner = ""
    ScannerTrivy     Scanner = "trivy"
    ScannerKubescape Scanner = "kubescape"
    ScannerBoth      Scanner = "both"
)

type ScannerStatus struct {
    Detected    Scanner        `json:"detected"`
    Trivy       *ScannerDetail `json:"trivy,omitempty"`
    Kubescape   *ScannerDetail `json:"kubescape,omitempty"`
    LastChecked string         `json:"lastChecked"`
}

type ScannerDetail struct {
    Available bool   `json:"available"`
    Namespace string `json:"namespace,omitempty"`
}

type SeveritySummary struct {
    Critical int `json:"critical"`
    High     int `json:"high"`
    Medium   int `json:"medium"`
    Low      int `json:"low"`
}

type WorkloadVulnSummary struct {
    Namespace   string          `json:"namespace"`
    Kind        string          `json:"kind"`
    Name        string          `json:"name"`
    Images      []ImageVulnInfo `json:"images"`
    Total       SeveritySummary `json:"total"`
    LastScanned string          `json:"lastScanned"`
    Scanner     Scanner         `json:"scanner"`
}

type ImageVulnInfo struct {
    Image      string          `json:"image"`
    Severities SeveritySummary `json:"severities"`
}
```

### Step 2 — Discovery

**New file:** `backend/internal/scanning/discovery.go`

Follow Phase 9 discoverer pattern:

- `ScannerDiscoverer` struct with `sync.RWMutex`, cached `*ScannerStatus`
- Constructor: `NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger)`
- `RunDiscoveryLoop(ctx)` — immediate + 5-min ticker
- `Discover(ctx)`:
  1. CRD check: `aquasecurity.github.io/v1alpha1` for `VulnerabilityReport` → Trivy
  2. CRD check: `spdx.softwarecomposition.org/v1beta1` for `VulnerabilityManifestSummary` → Kubescape
  3. Namespace probe: pods in `trivy-system` / `kubescape`
- `Status()` accessor

**New file:** `backend/internal/scanning/discovery_test.go`

### Step 3 — Trivy Adapter

**New file:** `backend/internal/scanning/trivy.go`

- `ListTrivyVulnSummaries(ctx, dynClient, namespace) ([]WorkloadVulnSummary, error)`:
  - List `vulnerabilityreports` (GVR: `aquasecurity.github.io/v1alpha1/vulnerabilityreports`) **in the specified namespace** (not cluster-wide)
  - Group by workload (from labels: `trivy-operator.resource.kind`, `.name`, `.namespace`)
  - Extract `report.summary.{criticalCount,highCount,mediumCount,lowCount}`
  - Extract image: `report.artifact.repository:tag`
  - Sum per-workload totals across containers

**New file:** `backend/internal/scanning/trivy_test.go` — test severity extraction and workload grouping

### Step 4 — Kubescape Adapter

**New file:** `backend/internal/scanning/kubescape.go`

- `ListKubescapeVulnSummaries(ctx, dynClient, namespace) ([]WorkloadVulnSummary, error)`:
  - List `vulnerabilitysummaries` (GVR: `spdx.softwarecomposition.org/v1beta1/vulnerabilitysummaries`) **in the specified namespace**
  - Extract `spec.severities.{critical,high,medium,low}.all`
  - Map workload from labels: `kubescape.io/workload-kind`, `.name`, `.namespace`

**New file:** `backend/internal/scanning/kubescape_test.go` — test severity extraction

### Step 5 — Handler

**New file:** `backend/internal/scanning/handler.go`

```go
type Handler struct {
    K8sClient     *k8s.ClientFactory
    Discoverer    *ScannerDiscoverer
    AccessChecker *resources.AccessChecker
    Logger        *slog.Logger

    fetchGroup singleflight.Group
    cacheMu    sync.RWMutex
    nsCache    map[string]*cachedNSData // keyed by namespace
}

type cachedNSData struct {
    vulns     []WorkloadVulnSummary
    fetchedAt time.Time
}
```

**Namespace-scoped caching:** Singleflight keyed by namespace (`"vulns:" + namespace`). 30s TTL per namespace. This avoids a cluster-wide LIST of potentially 6000+ VulnerabilityReports.

**RBAC filtering:** Use `CanAccessGroupResource` with:
- Trivy: `aquasecurity.github.io` / `vulnerabilityreports`
- Kubescape: `spdx.softwarecomposition.org` / `vulnerabilitysummaries`

**Handlers:**

1. `HandleStatus` — `GET /api/v1/scanning/status`
   - Returns `ScannerStatus`
   - Non-admin: strip namespace details

2. `HandleVulnerabilities` — `GET /api/v1/scanning/vulnerabilities?namespace=X`
   - **Requires `namespace` param** (no cluster-wide listing to avoid memory issues)
   - Fetch via namespace-scoped cache, merge Trivy + Kubescape results
   - RBAC check for the namespace before fetching
   - Sort by total critical+high descending
   - Response includes inline summary counts in metadata (computed from results)

### Step 6 — Wiring

**Route registration:**
```go
func (s *Server) registerScanningRoutes(ar chi.Router) {
    h := s.ScanningHandler
    ar.Route("/scanning", func(sr chi.Router) {
        sr.Use(middleware.RateLimit(s.YAMLRateLimiter))
        sr.Get("/status", h.HandleStatus)
        sr.Get("/vulnerabilities", h.HandleVulnerabilities)
    })
}
```

**Files to modify:**
- `backend/cmd/kubecenter/main.go`
- `backend/internal/server/server.go`
- `backend/internal/server/routes.go`

---

## Frontend

### Step 7 — Navigation + Types

**`frontend/lib/constants.ts`** — add tab to Security section:
```ts
{ label: "Vulnerabilities", href: "/security/vulnerabilities" },
```

**New file:** `frontend/lib/scanning-types.ts` — TS interfaces matching backend types

### Step 8 — Vulnerability Dashboard

**`frontend/islands/VulnerabilityDashboard.tsx`** (~350 LOC)

Follow PolicyDashboard / GitOpsApplications pattern:

- Fetch `/v1/scanning/status` + `/v1/scanning/vulnerabilities?namespace=X` in parallel
- **Namespace required** — default to selected namespace from global namespace selector, show prompt if "All Namespaces"
- Scanner status banner: which scanner(s) detected, or install guidance (Trivy Operator + Kubescape)
- **Inline summary counts** at top: critical/high/medium/low totals computed client-side from results
- Workload table: name, kind, images (truncated), critical (red), high (orange), medium (yellow), low (gray), scanner badge, last scanned
- Search/filter by name, scanner
- Sort by critical+high count descending (worst first)
- Pagination (PAGE_SIZE=100) + Refresh button
- If no scanner detected: show install links

### Step 9 — Route + Entry Points

**`frontend/routes/security/vulnerabilities.tsx`** — route with SubNav

Note: this does NOT conflict with `/security/violations` (policy violations). Different word.

**Command palette:** "View Vulnerabilities" quick action

**Files to create:**
- `frontend/lib/scanning-types.ts`
- `frontend/components/ui/ScanBadges.tsx`
- `frontend/islands/VulnerabilityDashboard.tsx`
- `frontend/routes/security/vulnerabilities.tsx`

**Files to modify:**
- `frontend/lib/constants.ts`
- `frontend/islands/CommandPalette.tsx`

---

## File Count Summary

| Area | New Files | Modified Files |
|------|-----------|---------------|
| Backend | 7 (types, discovery, discovery_test, trivy, trivy_test, kubescape, kubescape_test, handler) | 3 |
| Frontend | 4 (types, badges, island, route) | 2 |
| **Total** | **11** | **5** |

## API Surface

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/scanning/status` | GET | Scanner detection status |
| `/scanning/vulnerabilities?namespace=X` | GET | Per-workload vuln summaries (namespace-scoped) |
| **Total** | **2** | |

## CRD References

| Scanner | CRD | GVR |
|---------|-----|-----|
| Trivy | VulnerabilityReport | `aquasecurity.github.io/v1alpha1/vulnerabilityreports` |
| Kubescape | VulnerabilitySummary | `spdx.softwarecomposition.org/v1beta1/vulnerabilitysummaries` |

## Graceful Degradation

- **No scanner:** 200 with empty data. Frontend shows install guidance.
- **Trivy only / Kubescape only:** Data from detected scanner only.
- **Both:** Merged, scanner badge indicates source.
- **No namespace selected:** Frontend prompts user to select a namespace.

## Deferred Work

- Config audit dashboard (requires dedup strategy with Phase 8 policy violations)
- Compliance framework scores (Trivy ClusterComplianceReport, Kubescape GeneralReport)
- Per-CVE vulnerability detail page (users can browse raw CRDs for now)
- Cluster-wide summary endpoint (add when namespace-scoped proves insufficient)
- SBOM viewer (CycloneDX/SPDX)
- Kubescape "relevant vulnerabilities" (reachability analysis)
- Exposed secret reports (Trivy ExposedSecretReport)
- RBAC assessment reports (Trivy RbacAssessmentReport)
- Vulnerability trend tracking (DB-backed historical counts)
