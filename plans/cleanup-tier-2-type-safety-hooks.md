# Tier 2: Type Safety & Badge Consolidation

**Type:** refactor
**Parent:** `plans/cleanup-2026-04-15-plan.md` (Tier 1 COMPLETE)
**Date:** 2026-04-16
**Estimated PRs:** 2 (PR-E, PR-F), sequential, each ≤5 files per phase

---

## Overview

Tier 2 addresses two clusters of medium-risk technical debt identified during
the 8-agent codebase audit (2026-04-15). Each PR is independently shippable.

**Post-review revision (2026-04-16):** Three parallel reviewers (DHH, Kieran,
Simplicity) unanimously recommended dropping `useWizardForm`, `useApi`, and
folding badge consolidation into PR-F. Original PR-G scope eliminated.

---

## PR-E: Shared Condition Helpers (Backend Go)

### Problem

Five call sites across `policy/`, `gitops/`, and `notification/` parse
Kubernetes-style `status.conditions[]` by asserting `.(map[string]interface{})`
and manually extracting fields. The exact same shape
(`type, status, reason, message, lastTransitionTime`) is duplicated across
packages with no shared type.

### Implementation

**Phase 1: Create shared types (1 file)**

Add to an existing package — `backend/internal/k8s/conditions.go` (not a new
`shared/` package — per simplicity review, 2 types + 2 helpers don't justify
a new package):

```go
// Condition mirrors metav1.Condition for CRD status parsing via dynamic client.
type Condition struct {
    Type               string `json:"type"`
    Status             string `json:"status"`
    Reason             string `json:"reason,omitempty"`
    Message            string `json:"message,omitempty"`
    LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// ExtractConditions unmarshals a conditions slice from unstructured status.
func ExtractConditions(obj map[string]interface{}, path ...string) []Condition

// FindCondition returns the first condition matching condType, or nil.
func FindCondition(conditions []Condition, condType string) *Condition
```

Leave `EventSourceRef` in `notification/types.go` — it's notification-only.

**Phase 2: Refactor consumers (4 files, ≤5 per phase)**

| File | Lines | Current Pattern | Refactored To |
|------|-------|-----------------|---------------|
| `notification/flux_notifications.go` | 61-68 | `m.(map[string]interface{})` → manual field reads | `k8s.ExtractConditions(status)` |
| `gitops/flux.go` | 278-287 | Same manual extraction | `k8s.ExtractConditions(status)` |
| `gitops/argocd.go` | 405-414, 471-478 | Same pattern (2 sites) | `k8s.FindCondition(conditions, "Ready")` |
| `policy/kyverno.go` | 68-77, 91 | Partial condition reads | `k8s.FindCondition(conditions, "Ready")` |

Consolidate `gateway/types.go:85` Condition struct into the new shared one.

**Not refactored (domain-specific, leave local):**
- PolicyReport result format, Gatekeeper violations, AppSet generators,
  Flux inventory parsing, Helm history entries

### Acceptance Criteria

- [ ] `k8s.ExtractConditions()` and `FindCondition()` replace 5 assertion sites
- [ ] `gateway/types.go` Condition consolidated
- [ ] Zero `map[string]interface{}` condition-parsing remains in targeted files
- [ ] `go vet ./...` clean
- [ ] `go test ./internal/k8s/... ./internal/gitops/... ./internal/policy/... ./internal/notification/...` passes
- [ ] No API response shape changes (JSON output identical)

---

## PR-F: Frontend Type Safety & Badge Consolidation

### Problem

1. `ResourceDetail.tsx` has 3 `as any` casts for pod/workload spec access.
2. `K8sResource` base interface lacks `kind` field, undermining type narrowing.
3. `DaemonSet` and `StatefulSet` interfaces missing `template.spec` fields.
4. `api.ts` refresh response is untyped.
5. `SEVERITY_COLORS` duplicated identically in PolicyBadges and ScanBadges.
6. `ColorBadge` component exists only in PolicyBadges but reimplemented locally
   in CertificateBadges.

### Implementation

**Phase 1: Type interface fixes (1 file)**

In `frontend/lib/k8s-types.ts`:
- Add `kind?: string` to `K8sResource` base interface
- Add `template: { spec: { containers: Container[] } }` to `StatefulSet.spec`
- Add `template: { spec: { containers: Container[] } }` to `DaemonSet.spec`

**Phase 2: Kill `as any` casts + type refresh response (3 files)**

| File | Line | Current | Replacement |
|------|------|---------|-------------|
| `ResourceDetail.tsx` | 613 | `resource.value as any` | Plain assertion to `Deployment` (kind check already in scope) |
| `ResourceDetail.tsx` | 625, 647 | `resource.value as any` | Plain assertion to `Pod` (kind check already in scope) |
| `api.ts` | 72 | `await res.json()` (untyped) | `await res.json() as APIResponse<{ accessToken: string }>` |
| `resource-columns.ts` | 386, 443 | `r as any` | Direct typed access (DaemonSet interface now complete) |

Per Kieran's review: use existing `APIResponse<T>` generic envelope instead of
a one-off `RefreshTokenResponse` type. Type guards are redundant where kind
string checks already exist in scope — plain assertions are sufficient.

**Phase 3: Badge consolidation (3 files)**

Create `frontend/lib/badge-colors.ts`:
```typescript
export const SEVERITY_COLORS: Record<string, string> = {
  critical: "var(--danger)",
  high: "var(--warning)",
  medium: "var(--accent)",
  low: "var(--text-muted)",
  unknown: "var(--text-muted)",
};
```

Extract `ColorBadge` from `PolicyBadges.tsx` to `components/ui/ColorBadge.tsx`.
Update:
- `PolicyBadges.tsx` → import from `badge-colors.ts` + `ColorBadge.tsx`
- `ScanBadges.tsx` → import from `badge-colors.ts` (delete local duplicate)
- `CertificateBadges.tsx` → import `ColorBadge` (delete local reimplementation)

### Acceptance Criteria

- [ ] Zero `as any` in `ResourceDetail.tsx` and `resource-columns.ts`
- [ ] `api.ts` refresh response typed via `APIResponse<T>`
- [ ] `K8sResource` has `kind` field
- [ ] `SEVERITY_COLORS` defined once, imported by PolicyBadges + ScanBadges
- [ ] `ColorBadge` extracted to standalone component
- [ ] `deno lint` + `deno fmt --check` clean
- [ ] E2E tests pass

---

## Dropped Scope (per review)

| Item | Reason |
|------|--------|
| `useWizardForm<T>` hook | All 3 reviewers: stable repeated code, not active tech debt. Hook would become a god-hook. 49 redundant signals across 7 wizards is harmless. |
| `useApi<T>` hook | Plan itself marked optional. 3 signals + fetch is not boilerplate. Risk of reinventing TanStack Query. |
| `shared/` package | 2 types don't justify a new package. Use existing `k8s/` package. |
| `EventSourceRef` move | Notification-only. No second consumer. |

---

## Execution Order

```
PR-E (Go backend types)  ──→  review + merge
PR-F (TS types + badges)  ──→  review + merge  (independent of PR-E)
```

Both are independent. Execute sequentially per CLAUDE.md phased execution rules.

---

## Risk Assessment

| PR | Risk | Mitigation |
|----|------|-----------|
| PR-E | Import cycle: `k8s/` → already imported by `policy/`, `gitops/`, `notification/` | Verify `k8s/` doesn't import from those packages (it shouldn't) |
| PR-E | Condition field mismatch between packages | Unit tests with real CRD fixtures |
| PR-F | Kind string casing (plural vs singular) | Normalize at assertion sites; match existing codebase convention |
| PR-F | ColorBadge extraction breaks existing imports | Grep all `ColorBadge` consumers before extracting |
