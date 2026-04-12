# Cert-Manager Integration Implementation Plan (Phase 11A) — Revised

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship cert-manager observatory + lifecycle actions — Certificates/Issuers/ClusterIssuers list + detail (nested CertificateRequests/Orders/Challenges), force-renew and re-issue, and a background expiry poller emitting threshold events to Notification Center.

**Architecture:** New `backend/internal/certmanager/` package following the Velero/Policy/GitOps CRD-discovery pattern. Dynamic client. User impersonation on handler calls, service account on the background poller. Singleflight + 30s cache on list endpoints. Per-user RBAC via `AccessChecker.CanAccessGroupResource`. Poller uses `map[uid]threshold` dedupe. Frontend uses `@preact/signals`, Tailwind semantic tokens, `apiGet<T>` from `@/lib/api.ts`.

**Tech Stack:** Go 1.26, chi, client-go dynamic client, singleflight; Deno 2.x + Fresh 2.x, Preact signals, Tailwind v4 semantic tokens; PostgreSQL untouched.

**Spec:** `docs/superpowers/specs/2026-04-11-cert-manager-integration-design.md`

---

## Resolved Method Signatures

These were verified against the codebase — implementers should use exactly these:

```
# Getting user from request context:
user, ok := httputil.RequireUser(w, r)   // returns (*auth.User, bool); writes 401 if missing

# Impersonated dynamic client:
dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)

# Impersonated typed client:
typedClient, err := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)

# Base (service-account) dynamic client:
dyn := h.K8sClient.BaseDynamicClient()

# Discovery client:
disco := h.K8sClient.DiscoveryClient()

# RBAC check:
can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "get", "cert-manager.io", "certificates", namespace)

# Writing JSON response:
httputil.WriteData(w, data)                    // wraps in {"data": ...}
httputil.WriteError(w, http.StatusFoo, "msg", "detail")

# Audit logging (fire-and-forget):
_ = h.AuditLogger.Log(r.Context(), audit.Entry{
    Timestamp:         time.Now(),
    ClusterID:         middleware.ClusterIDFromContext(r.Context()),
    User:              user.Username,
    SourceIP:          r.RemoteAddr,
    Action:            audit.ActionCertRenew,     // new constants to add
    ResourceKind:      "Certificate",
    ResourceNamespace: ns,
    ResourceName:      name,
    Result:            audit.ResultSuccess,
})

# Notification emit (fire-and-forget):
go h.NotifService.Emit(context.Background(), notifications.Notification{
    Source:   notifications.SourceCertManager,
    Severity: notifications.SeverityWarning,
    Title:    "...",
    Message:  "...",
})

# Frontend API calls:
apiGet<T>("/v1/certificates/certificates")      // from @/lib/api.ts
apiPost<T>("/v1/certificates/certificates/ns/name/renew", {})

# Frontend state:
const loading = useSignal(true);                // from @preact/signals
const items = useSignal<Certificate[]>([]);
```

---

## File Structure

### Backend — create (4 files + tests)

- `backend/internal/certmanager/types.go` — GVR constants, normalized types, CertManagerStatus, Status enum, threshold constants
- `backend/internal/certmanager/normalize.go` — `normalizeCertificate`, `normalizeIssuer`, `normalizeCertRequest`, `normalizeOrder`, `normalizeChallenge`, `computeStatus`, `readReadyCondition`, `detectIssuerType`, and helpers
- `backend/internal/certmanager/handler.go` — `Handler` struct, `NewHandler`, `HandleStatus`, `HandleListCertificates`, `HandleGetCertificate`, `HandleListIssuers`, `HandleListClusterIssuers`, `HandleListExpiring`, `HandleRenew`, `HandleReissue`, `canAccess`, `auditLog`, `getImpersonatingClient`, `filterCertificatesByRBAC`, `filterIssuersByRBAC`, `canListClusterIssuers`, singleflight cache, `InvalidateCache`
- `backend/internal/certmanager/poller.go` — `Poller` struct, `NewPoller`, `Start`, `tick`, `check`, `emit`, threshold logic, `map[uid]threshold` dedupe

Tests:
- `backend/internal/certmanager/normalize_test.go` — table-driven: `computeStatus` (8 cases), `normalizeCertificate` (happy path + malformed conditions + nil status + expired), `normalizeIssuer` (ACME + CA + unknown type), `detectIssuerType` coverage
- `backend/internal/certmanager/poller_test.go` — table-driven: `thresholdBucket` boundaries, dedupe crossing detection (warning → critical → renewal → re-degrade), restart behavior (empty map re-emits)

### Backend — modify

- `backend/internal/notifications/types.go` — add `SourceCertManager Source = "certmanager"`
- `backend/internal/audit/logger.go` — add `ActionCertRenew Action = "cert_renew"`, `ActionCertReissue Action = "cert_reissue"`
- `backend/internal/server/server.go` — add `CertManagerHandler *certmanager.Handler` field
- `backend/internal/server/routes.go` — add `registerCertManagerRoutes` + call site
- `backend/cmd/kubecenter/main.go` — construct Discoverer/Handler/Poller, wire, start poller goroutine

### Frontend — create (3 islands + types + badges + 4 routes)

- `frontend/lib/certmanager-types.ts` — TS interfaces
- `frontend/components/ui/CertificateBadges.tsx` — `StatusBadge`, `IssuerTypeBadge`, `ExpiryBadge` (using Tailwind semantic tokens, not inline styles)
- `frontend/islands/CertificatesList.tsx` — list table with search, status/expiry badges, `?status=expiring` filter support
- `frontend/islands/CertificateDetail.tsx` — detail with nested CR/Order/Challenge tables, Renew/Re-issue buttons with confirm modal
- `frontend/islands/IssuersList.tsx` — unified Issuer + ClusterIssuer table
- `frontend/routes/security/certificates/index.tsx` — redirect to `./certificates`
- `frontend/routes/security/certificates/certificates.tsx` — list page shell
- `frontend/routes/security/certificates/certificates/[namespace]/[name].tsx` — detail page shell
- `frontend/routes/security/certificates/issuers.tsx` — issuers page shell

### Frontend — modify

- Security SubNav config — add "Certificates" tab
- `frontend/islands/CommandPalette.tsx` — add quick actions

### E2E + docs

- `e2e/tests/certificates.spec.ts` — one happy path (skip if unavailable)
- `CLAUDE.md` — Phase 11A entry, check off roadmap #7, add 11B placeholder

---

## Task 1: Backend package — types + normalization + tests

**Files:**
- Create: `backend/internal/certmanager/types.go`
- Create: `backend/internal/certmanager/normalize.go`
- Create: `backend/internal/certmanager/normalize_test.go`
- Modify: `backend/internal/notifications/types.go` — add `SourceCertManager`
- Modify: `backend/internal/audit/logger.go` — add `ActionCertRenew`, `ActionCertReissue`

- [ ] **Step 1: Add SourceCertManager to notifications/types.go**

Append to the `Source` const block after `SourceVelero`:

```go
SourceCertManager Source = "certmanager"
```

- [ ] **Step 2: Add audit actions to audit/logger.go**

Append to the `Action` const block after the Velero actions:

```go
ActionCertRenew  Action = "cert_renew"
ActionCertReissue Action = "cert_reissue"
```

- [ ] **Step 3: Write types.go**

GVR constants for `cert-manager.io/v1` (Certificate, Issuer, ClusterIssuer, CertificateRequest) and `acme.cert-manager.io/v1` (Order, Challenge). Normalized Go structs matching the spec. `Status` enum: Ready/Issuing/Failed/Expiring/Expired/Unknown. Threshold constants (7d critical, 30d warning). `CertManagerStatus` with Detected/Namespace/Version/LastChecked. `CertificateDetail` aggregate for the detail endpoint. `ExpiringCertificate` flat view.

- [ ] **Step 4: Write normalize.go**

`computeStatus(readyStatus, reason string, notAfter *time.Time) Status` — handles expired → expiring → ready → issuing → failed → unknown.

`normalizeCertificate(u *unstructured.Unstructured) (Certificate, error)` — extracts all fields from unstructured, computes DaysRemaining, flattens status via `computeStatus`.

`normalizeIssuer(u *unstructured.Unstructured, scope string) Issuer` — extracts type via `detectIssuerType` (checks spec.acme/ca/vault/selfSigned nested maps).

`normalizeCertRequest`, `normalizeOrder`, `normalizeChallenge` — simpler extractors using ownerReferences for parent linkage.

Helper functions: `readReadyCondition`, `stringFrom`, `parseTimeField`, `detectIssuerType`.

No `var _ = ...` placeholders. Every import must be real.

- [ ] **Step 5: Write normalize_test.go**

Table-driven tests:
- `TestComputeStatus` — 8 cases: ready-valid(60d), ready-expiring-warning(20d), ready-expiring-critical(3d), expired(-1h), issuing, failed, unknown, missing-ready-condition
- `TestNormalizeCertificate` — happy path (full cert with conditions/notAfter/dnsNames); malformed conditions (conditions not a slice); nil status (no status field at all); expired cert
- `TestNormalizeIssuer` — ACME type detection, CA type, unknown type, scope propagation
- `TestDetectIssuerType` — ACME/CA/Vault/SelfSigned/Unknown

- [ ] **Step 6: Run tests**

```bash
cd backend && go test ./internal/certmanager/... -v
```

Expected: all pass.

- [ ] **Step 7: Run go vet + build**

```bash
cd backend && go vet ./... && go build ./...
```

- [ ] **Step 8: Commit**

```bash
git add backend/internal/certmanager/types.go backend/internal/certmanager/normalize.go \
       backend/internal/certmanager/normalize_test.go \
       backend/internal/notifications/types.go backend/internal/audit/logger.go
git commit -m "feat(certmanager): types, normalization, and tests"
```

---

## Task 2: Backend — handler (reads + writes + RBAC + cache)

**Files:**
- Create: `backend/internal/certmanager/handler.go`

This is the largest single file. Contains: Handler struct, NewHandler, all 8 HTTP handlers, RBAC filter helpers, singleflight cache, impersonation helpers, audit helper.

- [ ] **Step 1: Write handler.go**

**Handler struct** (mirrors Velero handler exactly):

```go
type Handler struct {
    K8sClient     *k8s.ClientFactory
    Discoverer    *Discoverer
    AccessChecker *resources.AccessChecker
    AuditLogger   audit.Logger
    NotifService  *notifications.NotificationService
    Logger        *slog.Logger

    fetchGroup singleflight.Group
    cacheMu    sync.RWMutex
    cache      *cachedData
}
```

**Discoverer** embedded in handler.go (NOT a separate file — ~80 LOC):

```go
type Discoverer struct {
    disco  discovery.DiscoveryInterface
    kube   kubernetes.Interface
    logger *slog.Logger
    mu     sync.RWMutex
    status CertManagerStatus
}
```

`Probe()`: check `ServerResourcesForGroupVersion("cert-manager.io/v1")` for Certificate Kind. Probe `cert-manager` namespace deployments for version. Match velero discovery.go pattern exactly.

**RBAC helpers** (inline, not separate file):

```go
func (h *Handler) canAccess(ctx context.Context, user *auth.User, verb, resource, namespace string) bool
func (h *Handler) filterCertificatesByRBAC(ctx, user, certs) []Certificate
func (h *Handler) filterIssuersByRBAC(ctx, user, issuers) []Issuer
```

`canAccess` wraps `AccessChecker.CanAccessGroupResource` with `"cert-manager.io"` group, matching velero's `canAccess`.

**Read handlers:**

- `HandleStatus` — returns `Discoverer.Status()`
- `HandleListCertificates` — singleflight cache, RBAC filter, optional `?namespace=` query param
- `HandleGetCertificate` — impersonated dynamic client, fetches cert + nested CRs (label `cert-manager.io/certificate-name`), Orders (ownerRef from CRs), Challenges (ownerRef from Orders). Returns `CertificateDetail`.
- `HandleListIssuers` — from cache, RBAC filter
- `HandleListClusterIssuers` — from cache, cluster-scoped RBAC check first
- `HandleListExpiring` — from cache, filter to `DaysRemaining <= 30`, sorted by days ascending

**Write handlers:**

- `HandleRenew` — RBAC pre-check via `canAccess(ctx, user, "patch", "certificates", ns)`. Read-modify-write on status subresource: GET cert, upsert Issuing=True condition (preserving all other conditions), `UpdateStatus` (PUT). Set `observedGeneration` to `cert.GetGeneration()`. Preserve `lastTransitionTime` if Issuing was already True. Retry once on 409 Conflict. Audit log with `ActionCertRenew`.
- `HandleReissue` — RBAC pre-check via `canAccess(ctx, user, "delete", "secrets", ns)`. GET cert, extract `spec.secretName`, GET the Secret, **verify ownerReference points to this Certificate's UID** before deletion. If no matching ownerRef, return 400 "secret not owned by this certificate". Delete Secret via `ClientForUser` typed client. Audit log with `ActionCertReissue`.

**Cache** (singleflight + 30s TTL, same pattern as Velero):

```go
func (h *Handler) getCached(ctx) (*cachedData, error)
func (h *Handler) fetchAll(ctx) (*cachedData, error)  // BaseDynamicClient(), list all certs/issuers/clusterissuers
func (h *Handler) InvalidateCache()
```

**Important correctness notes for implementer:**
- `HandleRenew` must use `UpdateStatus` (PUT on `/status` subresource), NOT JSON Merge Patch — merge patch replaces the entire conditions array and would wipe Ready.
- Namespace filter on list uses a **new slice**, not `filtered[:0]` (that mutates the backing array from cache).
- Search filter should use `.toLowerCase()` equivalents — Go side doesn't need this, frontend does.

- [ ] **Step 2: Verify compile**

```bash
cd backend && go build ./internal/certmanager/...
```

Fix any errors. Discoverer needs import of `k8s.io/client-go/discovery`, `k8s.io/client-go/kubernetes`.

- [ ] **Step 3: Verify the full backend builds**

```bash
cd backend && go vet ./... && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add backend/internal/certmanager/handler.go
git commit -m "feat(certmanager): handler with reads, writes, RBAC, singleflight cache"
```

---

## Task 3: Backend — expiry poller + tests

**Files:**
- Create: `backend/internal/certmanager/poller.go`
- Create: `backend/internal/certmanager/poller_test.go`

- [ ] **Step 1: Write poller_test.go first (TDD)**

```go
func TestThresholdBucket(t *testing.T) {
    // Table-driven: 60d→none, 30d→warning, 29d→warning, 8d→warning, 7d→critical, 0d→critical, -1d→expired
}

func TestDedupeEmitOncePerCrossing(t *testing.T) {
    // 1. First tick at warning(25d) → emits 1 warning
    // 2. Same bucket next tick → emits nothing
    // 3. Cross to critical(5d) → emits 1 critical
    // 4. Renewal: advance to 60d (none) → emits nothing, clears entry
    // 5. Re-degrade to warning(20d) → emits 1 warning (entry was cleared by renewal)
}

func TestDedupeRestartsFromEmpty(t *testing.T) {
    // Fresh poller (empty map) with cert at critical → emits 1 critical
    // This pins the restart behavior: re-emit is expected after process restart
}
```

- [ ] **Step 2: Run tests to confirm failure**

```bash
cd backend && go test ./internal/certmanager/ -run "TestThreshold|TestDedupe" -v
```

Expected: compile errors.

- [ ] **Step 3: Write poller.go**

**Dedupe map**: `map[string]threshold` keyed by cert UID. Value is the *bucket that was last emitted*. Emit when `current != prev`. Clear entry when bucket returns to `thresholdNone` (cert renewed, notAfter advanced).

```go
type threshold int
const (
    thresholdNone threshold = iota
    thresholdWarning
    thresholdCritical
    thresholdExpired
)
```

`thresholdBucket(days int) threshold` — the only branching logic.

`check(c Certificate) []emitRecord` — compares `thresholdBucket(*c.DaysRemaining)` to `p.dedupe[c.UID]`. Updates map. Returns 0 or 1 records.

`emit(ctx, rec)` — calls `p.notifService.Emit(ctx, notifications.Notification{...})` directly (no adapter layer). Maps threshold to `SeverityWarning` or `SeverityCritical`. Event `ResourceKind` is `"certificate.expiring"` or `"certificate.expired"`.

`Start(ctx)` — ticker at 60s, calls `tick()` which checks `disc.IsAvailable()` then lists all certs via `BaseDynamicClient()`, normalizes, runs `check()` on each.

`newPollerForTest()` — returns a Poller with nil k8s/disc/notifService (only `check()` uses the dedupe map, which is all the test needs). Keep this minimal — zero-value with initialized map only.

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/certmanager/ -run "TestThreshold|TestDedupe" -v
```

Expected: all pass.

- [ ] **Step 5: Run full package tests**

```bash
cd backend && go test ./internal/certmanager/... -v && go vet ./internal/certmanager/...
```

- [ ] **Step 6: Commit**

```bash
git add backend/internal/certmanager/poller.go backend/internal/certmanager/poller_test.go
git commit -m "feat(certmanager): expiry poller with dedupe + notification emit"
```

---

## Task 4: Backend — wire into server + routes

**Files:**
- Modify: `backend/internal/server/server.go` — add `CertManagerHandler *certmanager.Handler` field
- Modify: `backend/internal/server/routes.go` — add `registerCertManagerRoutes` + call site
- Modify: `backend/cmd/kubecenter/main.go` — construct and wire

- [ ] **Step 1: Read server.go to find Server struct and VeleroHandler field**

Grep for `VeleroHandler` in server.go. Add `CertManagerHandler` next to it.

- [ ] **Step 2: Add registerCertManagerRoutes to routes.go**

Place right after `registerVeleroRoutes`. Match the exact shape:

```go
func (s *Server) registerCertManagerRoutes(ar chi.Router) {
    h := s.CertManagerHandler
    ar.Route("/certificates", func(cr chi.Router) {
        cr.Get("/status", h.HandleStatus)
        cr.Get("/certificates", h.HandleListCertificates)
        cr.With(resources.ValidateURLParams).Get("/certificates/{namespace}/{name}", h.HandleGetCertificate)
        cr.Get("/issuers", h.HandleListIssuers)
        cr.Get("/clusterissuers", h.HandleListClusterIssuers)
        cr.Get("/expiring", h.HandleListExpiring)

        yamlRL := s.YAMLRateLimiter
        if yamlRL == nil { yamlRL = s.RateLimiter }
        cr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).
            Post("/certificates/{namespace}/{name}/renew", h.HandleRenew)
        cr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).
            Post("/certificates/{namespace}/{name}/reissue", h.HandleReissue)
    })
}
```

- [ ] **Step 3: Wire call site**

After `if s.VeleroHandler != nil { s.registerVeleroRoutes(ar) }`:

```go
if s.CertManagerHandler != nil {
    s.registerCertManagerRoutes(ar)
}
```

- [ ] **Step 4: Construct in main.go**

After Velero construction block:

```go
cmDisc := certmanager.NewDiscoverer(k8sClient.DiscoveryClient(), k8sClient.BaseClientset(), logger)
cmHandler := certmanager.NewHandler(k8sClient, cmDisc, accessChecker, auditLogger, notifService, logger)
cmPoller := certmanager.NewPoller(k8sClient, cmDisc, notifService, logger)
go cmPoller.Start(ctx)
srv.CertManagerHandler = cmHandler
```

Adjust variable names to match what main.go actually uses. Read main.go's Velero block for the exact variable names.

- [ ] **Step 5: Build entire backend**

```bash
cd backend && go build ./... && go vet ./... && go test ./...
```

- [ ] **Step 6: Commit**

```bash
git add backend/internal/server/ backend/cmd/kubecenter/main.go
git commit -m "feat(certmanager): wire handler, poller, routes into server"
```

---

## Task 5: Frontend — types + badges + CertificatesList island

**Files:**
- Create: `frontend/lib/certmanager-types.ts`
- Create: `frontend/components/ui/CertificateBadges.tsx`
- Create: `frontend/islands/CertificatesList.tsx`

- [ ] **Step 1: Read existing patterns**

Read `frontend/lib/policy-types.ts` for TS type pattern. Read `frontend/components/ui/PolicyBadges.tsx` for badge pattern. Read `frontend/islands/PolicyDashboard.tsx` for island data-fetching pattern (`useSignal`, `apiGet`, `IS_BROWSER` guard, Tailwind semantic tokens).

- [ ] **Step 2: Write certmanager-types.ts**

Same interfaces as the spec — Certificate, Issuer, CertificateRequest, Order, Challenge, CertificateDetail, ExpiringCertificate, CertManagerStatus, CertStatus union, IssuerRef.

- [ ] **Step 3: Write CertificateBadges.tsx**

Using **Tailwind semantic token classes** (NOT inline `style={{}}`):

```tsx
// StatusBadge — pill using: text-success, text-warning, text-danger, text-brand, text-text-muted
//   bg-success/10, bg-warning/10, bg-danger/10, bg-brand/10, bg-text-muted/10
// ExpiryBadge — shows "Xd left" with text-danger / text-warning / text-text-muted
// IssuerTypeBadge — ACME/CA/Vault/SelfSigned badge
```

Match the exact class naming pattern from `PolicyBadges.tsx` (`EngineBadge`, `SeverityBadge` are good references).

- [ ] **Step 4: Write CertificatesList.tsx**

Using `@preact/signals` pattern from PolicyDashboard:

```tsx
import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusBadge, ExpiryBadge } from "@/components/ui/CertificateBadges.tsx";
import type { Certificate } from "@/lib/certmanager-types.ts";

export default function CertificatesList() {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const certs = useSignal<Certificate[]>([]);
  const search = useSignal("");

  async function fetchData() { ... apiGet<Certificate[]>("/v1/certificates/certificates") ... }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => { loading.value = false; });
  }, []);
```

Key: search filter uses `.toLowerCase().includes(q)` for case-insensitive matching.

Support `?status=expiring` URL param to pre-filter to expiring certs (replaces the separate ExpiryDashboard page). Check `globalThis.location?.search` in the effect.

- [ ] **Step 5: Type-check and format**

```bash
cd frontend && deno check lib/certmanager-types.ts components/ui/CertificateBadges.tsx islands/CertificatesList.tsx
cd frontend && deno fmt lib/certmanager-types.ts components/ui/CertificateBadges.tsx islands/CertificatesList.tsx
```

- [ ] **Step 6: Commit**

```bash
git add frontend/lib/certmanager-types.ts frontend/components/ui/CertificateBadges.tsx frontend/islands/CertificatesList.tsx
git commit -m "feat(frontend): cert-manager types, badges, and CertificatesList island"
```

---

## Task 6: Frontend — CertificateDetail + IssuersList islands

**Files:**
- Create: `frontend/islands/CertificateDetail.tsx`
- Create: `frontend/islands/IssuersList.tsx`

- [ ] **Step 1: Write CertificateDetail.tsx**

Same `useSignal` + `apiGet` pattern. Fetches `/v1/certificates/certificates/{ns}/{name}`. Displays:
- Header: name + StatusBadge + ExpiryBadge
- Action buttons: Renew (accent button), Re-issue (danger button with confirmation modal)
- Confirmation modal for Re-issue with text: "Re-issue will delete Secret {secretName}. Applications using it will briefly lose TLS until re-issuance completes."
- Details section: namespace, issuer ref (link if possible), secret (link to `/workloads/secrets/{ns}/{secretName}`), DNS names, notBefore/notAfter/renewalTime
- Nested CertificateRequests table (if any)
- Nested Orders table (if any)
- Nested Challenges table (if any)

Actions call `apiPost("/v1/certificates/certificates/{ns}/{name}/renew", {})` and `apiPost("/v1/certificates/certificates/{ns}/{name}/reissue", {})`.

Use existing `Button` component from `@/components/ui/Button.tsx`.

- [ ] **Step 2: Write IssuersList.tsx**

Fetches both `/v1/certificates/issuers` and `/v1/certificates/clusterissuers` in parallel. Merges and displays in unified table with scope column. Uses `IssuerTypeBadge`. Ready status shown as text-success/text-danger.

- [ ] **Step 3: Type-check and format**

```bash
cd frontend && deno check islands/CertificateDetail.tsx islands/IssuersList.tsx
cd frontend && deno fmt islands/CertificateDetail.tsx islands/IssuersList.tsx
```

- [ ] **Step 4: Commit**

```bash
git add frontend/islands/CertificateDetail.tsx frontend/islands/IssuersList.tsx
git commit -m "feat(frontend): CertificateDetail and IssuersList islands"
```

---

## Task 7: Frontend — routes + SubNav + CommandPalette

**Files:**
- Create: `frontend/routes/security/certificates/index.tsx`
- Create: `frontend/routes/security/certificates/certificates.tsx`
- Create: `frontend/routes/security/certificates/certificates/[namespace]/[name].tsx`
- Create: `frontend/routes/security/certificates/issuers.tsx`
- Modify: Security SubNav config file (grep for "Policies" tab to find it)
- Modify: `frontend/islands/CommandPalette.tsx`

- [ ] **Step 1: Read existing security routes for the shell pattern**

Read `frontend/routes/security/policies.tsx` for the page shell structure. Read the security SubNav config (grep for it).

- [ ] **Step 2: Write routes**

- `index.tsx`: redirect handler returning 302 to `./certificates`
- `certificates.tsx`: page shell rendering `<CertificatesList />`
- `certificates/[namespace]/[name].tsx`: page shell extracting params, rendering `<CertificateDetail namespace={ns} name={name} />`
- `issuers.tsx`: page shell rendering `<IssuersList />`

All pages should match the existing page shell pattern (likely includes a layout wrapper, heading, etc.).

- [ ] **Step 3: Add Certificates tab to Security SubNav**

Find the config (grep for "Policies" or "Violations" in nav/SubNav files). Add entry for "Certificates" pointing to `/security/certificates/certificates`.

- [ ] **Step 4: Add CommandPalette entries**

Grep for "Policies" in CommandPalette.tsx. Add:
```tsx
{ label: "Certificates", href: "/security/certificates/certificates", section: "Security" },
{ label: "Expiring Certificates", href: "/security/certificates/certificates?status=expiring", section: "Security" },
```

- [ ] **Step 5: Type-check and format entire frontend**

```bash
cd frontend && deno fmt --check && deno lint
```

Fix any issues.

- [ ] **Step 6: Commit**

```bash
git add frontend/routes/security/certificates/ frontend/islands/CommandPalette.tsx frontend/components/nav/
git commit -m "feat(frontend): cert-manager routes, SubNav tab, command palette entries"
```

---

## Task 8: Frontend verification + build

**Files:** none

- [ ] **Step 1: Full frontend verification**

```bash
cd frontend && deno fmt --check && deno lint && deno task build
```

Fix any issues.

- [ ] **Step 2: Full backend verification**

```bash
cd backend && go vet ./... && go test ./... && go build ./...
```

- [ ] **Step 3: Start dev and smoke test**

```bash
make dev
```

Open `http://localhost:5173/security/certificates/certificates`. Verify: page renders, table shows (empty if no cert-manager), nav tab appears, command palette has entries, no console errors. If cert-manager is present on dev kubeconfig, verify list populates and detail page opens.

- [ ] **Step 4: Nothing to commit — verification only**

---

## Task 9: E2E test + docs

**Files:**
- Create: `e2e/tests/certificates.spec.ts`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Read existing e2e spec for skip pattern**

Read `e2e/tests/` directory for a test that skips on unavailable feature (Policy or Velero pattern).

- [ ] **Step 2: Write certificates.spec.ts**

```typescript
import { expect, test } from "@playwright/test";

test.describe("cert-manager", () => {
  test("certificate list page loads", async ({ page, request }) => {
    const statusResp = await request.get("/api/v1/certificates/status");
    const statusJson = await statusResp.json();
    test.skip(!statusJson.data?.detected, "cert-manager not installed");

    await page.goto("/security/certificates/certificates");
    await page.waitForLoadState("networkidle");
    await expect(page.locator("h1")).toContainText("Certificates");

    const rows = page.locator("tbody tr");
    if (await rows.count() > 0) {
      await rows.first().locator("a").first().click();
      await expect(page.locator("h1")).toBeVisible();
    }
  });
});
```

- [ ] **Step 3: Update CLAUDE.md**

Add Phase 11A entry under Build Progress. Check off roadmap #7. Add Phase 11B placeholder.

Read CLAUDE.md first to find exact insertion points.

- [ ] **Step 4: Run E2E locally**

```bash
cd e2e && npx playwright test tests/certificates.spec.ts
```

Expected: pass or skip.

- [ ] **Step 5: Commit**

```bash
git add e2e/tests/certificates.spec.ts CLAUDE.md
git commit -m "test(e2e): cert-manager happy path + docs update"
```

---

## Task 10: Final verification + PR

**Files:** none

- [ ] **Step 1: Full verification**

```bash
cd backend && go vet ./... && go test ./... && go build ./...
cd frontend && deno fmt --check && deno lint && deno task build
```

- [ ] **Step 2: Run /compounding-engineering:workflows:review**

Per CLAUDE.md: mandatory before merge. Fix any findings.

- [ ] **Step 3: Push and open PR**

```bash
git push -u origin feat/cert-manager-design
gh pr create --title "feat: cert-manager observatory + lifecycle (Phase 11A)" --body "$(cat <<'EOF'
## Summary
- `internal/certmanager/` package: CRD discovery, normalized types, singleflight-cached read handlers, user-impersonated detail with nested CR/Order/Challenge tree
- Renew (status subresource read-modify-write setting Issuing=True) and reissue (Secret delete with ownerRef validation) actions
- Background expiry poller (60s tick) emits certificate.expiring/expired events to Notification Center with map[uid]threshold dedupe
- 3 frontend islands (CertificatesList, CertificateDetail, IssuersList) using @preact/signals + Tailwind semantic tokens
- 4 routes under /security/certificates/* with SubNav tab + command palette

## Test plan
- [ ] `go vet ./... && go test ./... && go build ./...` clean
- [ ] `deno fmt --check && deno lint && deno task build` clean
- [ ] Homelab smoke: list, detail, renew action round-trip
- [ ] E2E: certificates.spec.ts passes or skips

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Watch CI**

```bash
gh run list --limit 1 && gh run view
```

---

## Self-Review

**Spec coverage:** All 6 spec sections mapped to tasks. Status/list/detail/issuers/clusterissuers/expiring endpoints → Task 2. Renew/reissue → Task 2. Poller + notification → Task 3. Frontend → Tasks 5-7. E2E + docs → Task 9.

**Correctness fixes from reviewers:**
- ✅ HandleRenew uses read-modify-write UpdateStatus, not MergePatchType
- ✅ HandleReissue validates Secret ownerReference before deletion
- ✅ Write actions pre-check RBAC before impersonated call
- ✅ No `var _ = ...` placeholders or broken generics
- ✅ Frontend uses useSignal/apiGet/Tailwind semantic tokens (not useState/api.get/inline styles)
- ✅ Search filter uses toLowerCase
- ✅ Poller dedupe simplified to map[uid]threshold
- ✅ All method signatures verified against codebase

**Files per task:** Task 1: 5 files, Task 2: 1 file, Task 3: 2 files, Task 4: 3 files, Task 5: 3 files, Task 6: 2 files, Task 7: ~5 files, Task 8: 0, Task 9: 2 files, Task 10: 0. All within 5-file limit.
