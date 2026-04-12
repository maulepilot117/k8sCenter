# Cert-Manager Integration — Design

**Date:** 2026-04-11
**Roadmap item:** #7 Cert-Manager integration
**Phase label:** Phase 11A (Observatory + Lifecycle). Follow-up Phase 11B (Creation Wizards) scoped at end.

## Goal

Surface cert-manager state inside k8sCenter so operators can (1) see every Certificate/Issuer/ClusterIssuer at a glance, (2) drill into stuck ACME flows via nested CertificateRequest/Order/Challenge views, (3) take one-click lifecycle actions (force-renew, re-issue), and (4) receive proactive expiry notifications through the existing Notification Center before certificates outage production.

This is Phase 11A — observatory + lifecycle. Creation wizards are Phase 11B and scoped separately below.

## Non-Goals

- Creation wizards for Certificate/Issuer/ClusterIssuer (Phase 11B)
- Configurable per-cert expiry thresholds (hardcoded 7d/30d in 11A)
- Remote-cluster expiry polling (11A polls local cluster only; remote cluster certs still listable via direct API)
- Managing cert-manager's own installation/upgrade (that's the operator's job)

## Architecture Overview

Cert-manager integration follows the CRD-discovery pattern established by Policy (PR #149), GitOps (Phase 9), and Velero (Phase 5 roadmap #5):

- New `backend/internal/certmanager/` package handles CRD-based feature detection and resource reading via the dynamic client.
- All user-facing k8s calls go through user impersonation. Background poller uses the service account (system loop).
- List endpoints use singleflight + 30s cache. Detail endpoints are uncached (user-impersonated, fresh).
- Per-user RBAC filtering via the existing `AccessChecker.CanAccessGroupResource`.
- Remote clusters use direct API calls only — no informers. Follows the existing multi-cluster rule.
- Three subsystems inside the package: **Inventory** (read), **Lifecycle actions** (write), **Expiry poller** (background → Notification Center).

## Backend Package Layout

```
backend/internal/certmanager/
├── discovery.go       # CRD detection for cert-manager.io/v1 group, sets Available flag
├── types.go           # Normalized: Certificate, Issuer, CertRequest, Order, Challenge
├── client.go          # Dynamic client wrappers, user impersonation helpers
├── handler.go         # HTTP handlers + singleflight 30s cache
├── actions.go         # Force-renew, re-issue logic
├── poller.go          # Expiry threshold poller + dedupe map
├── notifier.go        # Notification Center source adapter
├── rbac.go            # CanAccessGroupResource wiring for cert-manager CRDs
└── *_test.go          # Unit tests
```

### Normalized types

cert-manager's status is verbose (conditions array + phase + per-stage sub-conditions). We flatten to a single `Status` enum:

```
Ready | Issuing | Failed | Expiring | Expired | Unknown
```

`Expiring` is computed client-side from `NotAfter` against the warning threshold (30d). `Expired` when `NotAfter < now`. `Failed` when the Ready condition is `False` with a non-issuing reason.

### Wiring

Handler registered in `backend/internal/server/routes.go` under `/api/v1/certificates/*`. Poller launched from `backend/cmd/kubecenter/main.go` alongside the existing `ClusterProber`, gated on CRD availability (`discovery.Available()`).

## HTTP Endpoints

All under `/api/v1/certificates/`, all require auth. Writes require CSRF (`X-Requested-With`).

### Read

| Method | Path | Description |
|---|---|---|
| `GET` | `/certificates/status` | cert-manager availability, version, installed CRDs |
| `GET` | `/certificates/certificates?namespace=` | List Certificates (RBAC-filtered, cached) |
| `GET` | `/certificates/certificates/{ns}/{name}` | Detail with nested CR/Order/Challenge (impersonated, uncached) |
| `GET` | `/certificates/issuers?namespace=` | Issuer list |
| `GET` | `/certificates/clusterissuers` | ClusterIssuer list |
| `GET` | `/certificates/expiring?threshold=30d` | Flat list of certs crossing threshold (drives dashboard) |

### Write

| Method | Path | Description |
|---|---|---|
| `POST` | `/certificates/certificates/{ns}/{name}/renew` | Force renewal via status subresource patch adding `Issuing=True` condition (matches `cmctl renew`) |
| `POST` | `/certificates/certificates/{ns}/{name}/reissue` | Full re-issue: delete owned Secret (confirmation required in UI) |

Standard delete goes through the existing generic `/resources/certificates/...` handler — no custom endpoint.

### IDs

Certificates and Issuers have stable `namespace/name`. No composite IDs needed (unlike GitOps apps).

## Notification Source Integration

### Poller

- Goroutine ticks every 60s. Local cluster only in 11A.
- Service account (not user impersonation) — this is a system loop.
- For each Certificate with `NotAfter` set, compute `timeUntilExpiry` and bucket against thresholds: `[7d → critical, 30d → warning]`.
- **Threshold crossing detection:** Compare previous tick's bucket to current bucket for each cert UID. Emit only when the bucket transitions *into* a more severe level.
- **Dedupe map:** `map[string]time.Time` keyed by `<cert-uid>:<threshold>`. Entry set when emitted. Entry cleared when cert's `NotAfter` advances past the threshold (renewal detected). Reset on process restart — accepted trade-off. Notification Center rules may re-deliver once after restart; this is harmless.

### Event schemas

All events flow through the existing Notification Center source interface (same pattern as alerts/policy/GitOps/diagnostics from PR #162):

```json
{
  "source": "certmanager",
  "kind": "certificate.expiring",
  "severity": "warning" | "critical",
  "namespace": "...",
  "name": "...",
  "issuer": "...",
  "notAfter": "2026-05-11T00:00:00Z",
  "daysRemaining": 6,
  "resourceLink": "/security/certificates/<ns>/<name>"
}
```

Event kinds emitted:
- `certificate.expiring` — threshold crossing (warning/critical)
- `certificate.expired` — `NotAfter < now`, severity critical
- `certificate.failed` — Ready condition flips from True to False, severity critical

Dispatch targets (Slack/email/webhook) are configured by the user in the Notification Center UI — cert-manager only emits events.

### Configuration

Thresholds hardcoded in 11A: `[7*24h, 30*24h]`. Per-cert / per-issuer overrides are Phase 11B scope.

## Frontend

### Routes (`frontend/routes/security/certificates/`)

| Path | Purpose |
|---|---|
| `index.tsx` | Redirects to `./certificates` |
| `certificates.tsx` | List view, filterable by namespace/status/expiry window |
| `certificates/[namespace]/[name].tsx` | Detail with nested CR/Order/Challenge timeline |
| `issuers.tsx` | Combined Issuer + ClusterIssuer list with scope badge |
| `expiring.tsx` | Dedicated expiry dashboard (dashboard-card drill-down target) |

### Islands (`frontend/islands/`)

| Island | Responsibility |
|---|---|
| `CertificatesList.tsx` | Sortable table, status pill, days-until-expiry column with warning/critical bands |
| `CertificateDetail.tsx` | Status panel, DNS names, issuer link, Secret link, nested CR/Order/Challenge timeline, action buttons (Renew, Re-issue with confirm modal) |
| `IssuersList.tsx` | Unified list, `scope: Namespaced \| Cluster` column, type badge (ACME/CA/Vault/SelfSigned) |
| `ExpiryDashboard.tsx` | Big-number tiles (Expired / <7d / <30d), at-risk table, direct links |
| `CertificateStatusBanner.tsx` | On main Security index, shows critical expiry count if any |

### Shared modules

- `frontend/lib/certmanager-types.ts` — TypeScript interfaces mirroring normalized backend types
- `frontend/components/ui/CertificateBadges.tsx` — `StatusBadge`, `IssuerTypeBadge`, `ExpiryBadge`
- Follows the pattern from `lib/policy-types.ts` + `components/ui/PolicyBadges.tsx`

### Navigation

- Security SubNav gains a new **Certificates** tab with a live count (total certs + expiring secondary count)
- Command palette quick actions: "Certificates", "Expiring certificates"
- Secret link in `CertificateDetail` uses the existing shared `resourceHref` helper from `lib/k8s-links.ts`

### Theming

All colors reference CSS custom property tokens (`var(--success)`, `var(--warning)`, `var(--danger)`, `var(--accent)`) per Phase 6C design normalization. No hardcoded Tailwind color classes.

## RBAC

- `CanAccessGroupResource(user, "cert-manager.io", "certificates", verb)` gates list + detail
- Same for `issuers`, `clusterissuers`, `certificaterequests`, `orders`, `challenges`
- Write actions additionally require the corresponding verb (`patch` for renew, `delete` for reissue)
- Remote cluster writes impersonate through `ClusterRouter` — same as every other write path
- AccessChecker queries local cluster RBAC only (known limitation, same as rest of codebase; Kubernetes API enforces actual permissions)

## Testing

### Go unit tests (target ~30 tests)

- Discovery: CRD present/absent → `Available` flag correctness
- Type normalization: various condition combinations → correct `Status` enum
- Poller threshold math: table-driven crossing detection, edge cases (exactly at boundary, multi-tick stability)
- Dedupe map lifecycle: set → renewed → cleared, restart behavior
- RBAC filter: permitted vs. denied list responses
- Notifier event shape: each event kind serializes as documented

### Playwright E2E

One happy-path test in `e2e/tests/certificates.spec.ts`:
1. Navigate to `/security/certificates`
2. Skip if `/api/v1/certificates/status` reports `available: false`
3. Assert list renders, click first cert, detail panel opens, assert status field present

### Homelab smoke test

Per CLAUDE.md pre-merge rule: deploy to homelab, verify cert-manager CRDs detected, list loads, detail panel opens, renew action round-trips (if a real cert is present).

## Security Checklist

- [x] All endpoints require auth
- [x] All k8s operations use user impersonation (poller is the only exception and is read-only)
- [x] CSRF required on renew/reissue
- [x] Audit log captures renew/reissue via existing audit middleware
- [x] No secrets exposed in API responses (cert Secret contents only accessed through existing secret detail handler with its own audit trail)
- [x] Threshold poller does not expose certificate private key material

## Phase 11A Deliverables Summary

- `internal/certmanager/` Go package (8 files)
- 8 HTTP endpoints (6 read, 2 write)
- 5 frontend routes, 5 islands, shared types + badges
- Notification Center source registration with 3 event kinds
- ~30 unit tests + 1 Playwright test
- CLAUDE.md Phase 11A entry, nav section, command palette items
- Roadmap #7 checked off; Phase 11B entry queued behind it

## Phase 11B — Creation Wizards (Follow-up, Separate Plan)

Queued immediately after 11A merges. Separate design doc + plan.

Scope:
- **Certificate wizard** — DNS names (SAN list), issuer picker (references existing Issuers/ClusterIssuers), duration, renewBefore, keystore options
- **Issuer wizard** with sub-flows:
  - ACME HTTP01
  - ACME DNS01 (Cloudflare, Route53, Google Cloud DNS)
  - CA
  - SelfSigned
  - Vault
- **ClusterIssuer wizard** — same as Issuer with `scope: cluster`
- **Force-rotate action** — delete Secret + delete CertificateRequest (distinct from re-issue: rotates key material)
- **Configurable expiry thresholds** — per-issuer or per-cert via annotation (e.g., `kubecenter.io/expiry-warn: 14d`)

Pattern follows the Policy wizard work from PR #149: `backend/internal/wizard/` framework, `WizardInput` → generated YAML → server-side apply.
