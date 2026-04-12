# Cert-Manager Integration (Phase 11A)

Feature: Certificate inventory, issuer management, lifecycle actions (renew/re-issue), and proactive expiry notifications via cert-manager CRD integration.

**Priority:** #7 on roadmap
**Design Date:** 2026-04-11
**Status:** COMPLETE (PR #172)

---

## Overview

cert-manager integration that provides:

- **Certificate Inventory** — List all Certificates with status, issuer, DNS names, and expiry countdown
- **Issuer Management** — Unified view of Issuers and ClusterIssuers with type detection (ACME/CA/Vault/SelfSigned)
- **Certificate Detail** — Nested CertificateRequest/Order/Challenge timeline for ACME troubleshooting
- **Lifecycle Actions** — One-click renew (status subresource patch) and re-issue (Secret deletion with ownerRef validation)
- **Expiry Notifications** — Background poller (60s) emits threshold-crossing events to Notification Center with dedupe

---

## Architecture

```
                        ┌──────────────────────────────────────────┐
┌─────────────────┐     │  certmanager.Handler                     │
│  Dynamic Client │────▶│  - singleflight + cache (30s)            │
│  (user imperson)│     │  - RBAC filtering per user               │
└─────────────────┘     │  - generic filterByRBAC[T]               │
                        └───────────┬──────────────────────────────┘
                                    │
              ┌─────────────────────┼─────────────────────┐
              ▼                     ▼                     ▼
    ┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐
    │  Discoverer     │   │  Poller (60s)   │   │  Notification   │
    │  CRD probe +    │   │  map[uid]thresh │   │  Center source  │
    │  5min cache     │   │  + stale prune  │   │  (certmanager)  │
    └─────────────────┘   └─────────────────┘   └─────────────────┘
```

**Key Decisions:**
- Package `certmanager` (4 Go files: types, normalize, handler, discovery, poller + tests)
- CRD discovery via `ServerResourcesForGroupVersion("cert-manager.io/v1")`, 5-min stale cache
- Singleflight + 30s TTL for list endpoints; detail endpoint is uncached + user-impersonated
- Poller reuses handler cache to avoid duplicate cluster-wide list calls
- Renew uses read-modify-write on status subresource (UpdateStatus PUT), NOT JSON Merge Patch
- Reissue validates Secret ownerReference UID before deletion
- Write endpoints pre-check RBAC before impersonated k8s call (clean 403 vs opaque 500)
- Error responses scrubbed of internal details (logged server-side only)
- Label selectors built via `labels.Set{}.String()` for injection safety
- Frontend uses `@preact/signals`, `apiGet<T>`, Tailwind semantic tokens

---

## cert-manager CRDs

| CRD | API Group | Purpose |
|-----|-----------|---------|
| **Certificate** | `cert-manager.io/v1` | Desired certificate state + renewal config |
| **Issuer** | `cert-manager.io/v1` | Namespaced certificate authority |
| **ClusterIssuer** | `cert-manager.io/v1` | Cluster-scoped certificate authority |
| **CertificateRequest** | `cert-manager.io/v1` | Individual issuance request (owned by Certificate) |
| **Order** | `acme.cert-manager.io/v1` | ACME order (owned by CertificateRequest) |
| **Challenge** | `acme.cert-manager.io/v1` | ACME challenge (owned by Order) |

---

## HTTP Endpoints

All under `/api/v1/certificates/`, auth required, CSRF on writes.

### Read

| Method | Path | Description |
|---|---|---|
| GET | `/certificates/status` | cert-manager availability, version |
| GET | `/certificates/certificates?namespace=` | List (RBAC-filtered, cached) |
| GET | `/certificates/certificates/{ns}/{name}` | Detail with nested CR/Order/Challenge |
| GET | `/certificates/issuers?namespace=` | Issuer list |
| GET | `/certificates/clusterissuers` | ClusterIssuer list |
| GET | `/certificates/expiring` | Expiring certs sorted by urgency |

### Write

| Method | Path | Description |
|---|---|---|
| POST | `/certificates/certificates/{ns}/{name}/renew` | Force renewal via Issuing=True condition |
| POST | `/certificates/certificates/{ns}/{name}/reissue` | Re-issue via ownerRef-validated Secret delete |

---

## Frontend

### Islands
- `CertificatesList.tsx` — Searchable table with StatusBadge, ExpiryBadge, `?status=expiring` filter
- `CertificateDetail.tsx` — Detail with nested timeline tables, Renew/Re-issue buttons with confirmation
- `IssuersList.tsx` — Unified Issuer + ClusterIssuer table with IssuerTypeBadge

### Routes
- `/security/certificates` — list page
- `/security/certificates/{ns}/{name}` — detail page
- `/security/certificates/issuers` — issuers page

### Navigation
- Security SubNav: "Certificates" tab
- Command Palette: "View Certificates", "View Expiring Certificates"

---

## Expiry Poller

- 60s ticker, local cluster only (Phase 11A)
- Reuses handler cache (no duplicate API calls)
- Dedupe: `map[string]threshold` keyed by cert UID
- Stale UID pruning: entries for deleted certs removed each tick
- Thresholds: 30d warning, 7d critical (hardcoded; configurable in Phase 11B)
- Events: `certificate.expiring` (warning/critical), `certificate.expired`
- Notifications use generic messages (no namespace/name in text) to respect RBAC boundaries

---

## Testing

- 22 Go unit tests: computeStatus (8), normalizeCertificate (4), normalizeIssuer (3), detectIssuerType (5), thresholdBucket (7 boundary cases), dedupe state machine (2)
- 2 Playwright E2E tests (skip if cert-manager absent)
- Homelab smoke test per pre-merge rules

---

## Phase 11B (Follow-up)

Queued as separate spec + plan after 11A merges:
- Certificate/Issuer/ClusterIssuer creation wizards (ACME HTTP01/DNS01, CA, Vault, SelfSigned)
- Force-rotate action (delete Secret + CertificateRequest)
- Configurable per-cert/per-issuer expiry thresholds via annotation
