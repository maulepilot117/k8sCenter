# feat: Cert-Manager Wizards (Phase 11B)

## Overview

Phase 11A shipped the Cert-Manager Observatory (inventory, detail, renew/reissue, expiry poller). Phase 11B adds **creation wizards** for Certificate, Issuer, and ClusterIssuer resources following the established `WizardInput → YAML preview → server-side apply` pipeline used by 18 existing wizards.

Configurable expiry thresholds (originally part of this scope) have been **split into a sibling PR** — they share no code path with the wizard pipeline and land cleaner reviewed independently. Tracked separately as `plans/cert-manager-configurable-thresholds.md` (to be written).

Closes roadmap item **#7b** (wizard half).

## Problem Statement

Operators issuing a new TLS certificate today must hand-write YAML and `kubectl apply`. Cert-manager's CRD surface (ACME, CA, Vault, SelfSigned + per-issuer solver matrix) is the #1 misconfiguration source. GUI wizards are the same friction-killer that motivated the 18 existing wizards.

## Proposed Solution

Three wizard endpoints, two frontend islands (Issuer and ClusterIssuer share one island via a `scope` prop):

1. **Certificate wizard** — basic fields + collapsible advanced section in a single form.
2. **Issuer / ClusterIssuer wizard** — issuer-type picker (SelfSigned / ACME / CA / Vault), then one `IssuerFormStep` that branches internally on type.

**v1 ACME scope: HTTP01 ingress solver only.** DNS01 providers (Cloudflare, Route53, etc.) and Venafi deferred to Phase 11C when real demand appears.

## Technical Approach

### Backend

**New package `backend/internal/wizard/certmgr/`:**

- `certificate.go` — `CertificateInput` implementing `WizardInput` (Validate + ToYAML).
- `issuer.go` — `IssuerInput` with typed `IssuerScope` (constants `IssuerScopeNamespaced`, `IssuerScopeCluster`). Validates exactly one of {acme, ca, vault, selfSigned}.
- `certificate_test.go`, `issuer_test.go` — table-driven tests (see Validation Matrix below).

Package location (`wizard/certmgr/`) mirrors the existing 18-wizard density, avoids polluting the top-level `wizard` package.

**Route registration** in `backend/internal/server/routes.go` (kebab-case per CLAUDE.md convention):

```go
r.Post("/wizards/certificate/preview",     wizard.HandlePreview(func() WizardInput { return &certmgr.CertificateInput{} }))
r.Post("/wizards/issuer/preview",          wizard.HandlePreview(func() WizardInput { return &certmgr.IssuerInput{Scope: certmgr.IssuerScopeNamespaced} }))
r.Post("/wizards/cluster-issuer/preview",  wizard.HandlePreview(func() WizardInput { return &certmgr.IssuerInput{Scope: certmgr.IssuerScopeCluster} }))
```

**Handler-registration race (DHH catch):** current `wizard.HandlePreview(&CertificateInput{})` pattern shares a pointer across requests. Must refactor `HandlePreview` to accept a `func() WizardInput` factory that returns a fresh instance per call. Small change in `backend/internal/wizard/handler.go`; affects all 18 existing wizards but is backward-compatible (zero behavior change, just allocation moves into the factory).

### Frontend

**New islands** (`frontend/islands/`):

- `CertificateWizard.tsx` — single form step with `<details>` for advanced fields, then Review. Issuer dropdown loads `GET /issuers` + `GET /clusterissuers` (both RBAC-filtered server-side via Phase 11A's `CanAccessGroupResource`).
- `IssuerWizard.tsx` — `scope` prop drives namespace input visibility and `kind` emission. Steps: Type Picker (SelfSigned / ACME / CA / Vault radio cards), Form, Review.

**New wizard step components** (`frontend/components/wizard/`):

- `CertificateForm.tsx` — one file, `<details>`-based advanced collapse. No separate `BasicsStep`/`AdvancedStep` split.
- `IssuerTypePickerStep.tsx`, `IssuerFormStep.tsx` — the form branches internally by type (SelfSigned = empty body; ACME = HTTP01 inputs; CA = two Secret refs; Vault = server/path/auth).

**New routes:**

- `frontend/routes/security/certificates/new.tsx`
- `frontend/routes/security/certificates/issuers/new.tsx`
- `frontend/routes/security/certificates/cluster-issuers/new.tsx`

**Entry points:**

- "Create Certificate" button in `CertificatesList.tsx` header.
- "Create Issuer" / "Create ClusterIssuer" buttons in `IssuersList.tsx`.
- Command palette quick actions: `cert-manager: Create Certificate`, `cert-manager: Create Issuer`, `cert-manager: Create ClusterIssuer`.

All entry points gated on existing `CertManagerDiscoverer.IsInstalled()`.

### Validation Matrix

**CertificateInput:**

| Field | Rule |
|---|---|
| `name`, `namespace` | RFC 1123 label, ≤253 chars |
| `issuerRef.kind` | must be `Issuer` or `ClusterIssuer` (exact case) |
| `issuerRef.name` | required, RFC 1123 |
| `dnsNames ∪ commonName ∪ ipAddresses ∪ uris ∪ emailAddresses` | at least one non-empty |
| `dnsNames[i]` | RFC 1123 hostname; wildcard `*.x` only in leftmost label |
| `commonName` | ≤64 chars; warn if not in dnsNames |
| `duration` | Go `time.Duration`, ≥1h |
| `renewBefore` | ≥5m, strictly less than `duration` |
| `privateKey.algorithm × size` | `RSA ∈ {2048, 3072, 4096}`, `ECDSA ∈ {256, 384, 521}`, `Ed25519` ignores size |
| `privateKey.encoding` | `PKCS1` or `PKCS8` |
| `privateKey.rotationPolicy` | `Always` or `Never` |

**IssuerInput (type-specific):**

| Type | Required |
|---|---|
| `selfSigned` | (empty) |
| `ca` | `secretName` |
| `vault` | `server` (https://), `path`, exactly one of auth methods |
| `acme` | `server` (https:// only, reject http:// and RFC1918), valid RFC 5322 `email`, `privateKeySecretRef.name`, ≥1 solver with HTTP01 ingress |

Secrets are **not** pre-existence-checked at preview time (consistent with all 18 wizards — SSA catches missing resources at apply).

### YAML Example

Self-signed ClusterIssuer (ideal "first issuer" default):

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata: { name: selfsigned }
spec: { selfSigned: {} }
```

## Acceptance Criteria

### Certificate wizard

- [ ] `POST /wizards/certificate/preview` returns valid YAML for minimal input.
- [ ] Full Validation Matrix above enforced; table tests cover every row.
- [ ] Issuer dropdown lists both Issuers (current namespace) and all ClusterIssuers, grouped by `kind`, RBAC-filtered (user without `list` on `clusterissuers.cert-manager.io` sees only namespaced Issuers).
- [ ] Defaults pre-filled: `duration=2160h`, `renewBefore=360h`, `privateKey={RSA, 2048, Always}`, `usages=[digital signature, key encipherment, server auth]`.
- [ ] Review step shows YAML in Monaco read-only; Apply uses existing server-side apply endpoint.

### Issuer / ClusterIssuer wizard

- [ ] Type picker shows 4 cards (SelfSigned, ACME, CA, Vault). No "advanced types" toggle; Venafi is out of scope entirely.
- [ ] ACME form defaults to Let's Encrypt **staging**; `server` field is free-text but rejects `http://` and RFC1918 at preview time.
- [ ] HTTP01 ingress is the only solver in v1; UI does not render DNS01 options.
- [ ] ClusterIssuer route omits namespace input and emits `kind: ClusterIssuer`.

### Security / RBAC

- [ ] Issuer dropdown endpoints filter by `CanAccessGroupResource` (existing Phase 11A pattern).
- [ ] Apply step for ClusterIssuer requires cluster-scope `create` on `clusterissuers.cert-manager.io` (impersonated; k8s API enforces, but explicit E2E check).
- [ ] Wizard preview never performs write actions; apply goes through existing SSA endpoint which already audits.
- [ ] All k8s calls impersonate caller (no service-account writes).

### Non-functional

- [ ] `go vet`, `go test ./...`, `deno lint`, `deno fmt --check`, `deno task build` all pass.
- [ ] `wizard.HandlePreview` factory refactor lands first as a prep commit; all 18 existing wizards continue to pass their tests unchanged.
- [ ] Zero hardcoded Tailwind color classes in new components (Phase 6C compliance).
- [ ] E2E smoke test (`e2e/cert-manager-wizards.spec.ts`): create SelfSigned ClusterIssuer → create Certificate referencing it → verify Certificate appears in list and Ready=True within 30s.

## Dependencies & Risks

- **Dependency:** cert-manager v1.x CRDs. Gated via `CertManagerDiscoverer.IsInstalled()`.
- **Risk (low):** `HandlePreview` factory refactor touches all 18 wizard registrations. Mitigated by Phase 0 commit + full test suite.
- **Risk (low):** ACME `server` URL reachability is cert-manager's problem, not ours. We only enforce scheme + non-private-IP at preview.

## Out of Scope (Deferred)

- **Configurable expiry thresholds** — sibling PR.
- **Venafi issuer type.**
- **ACME DNS01 solvers** (any provider) — Phase 11C.
- **Gateway API HTTP01 solver** (`gatewayHTTPRoute`) — Phase 11C.
- **Advanced Certificate fields:** `keystores` (PKCS12/JKS), `literalSubject`, `otherNames`, `nameConstraints`, `secretTemplate`, `additionalOutputFormats`. Users needing these edit YAML directly.

## References

### Internal

- Generic wizard pipeline: `backend/internal/wizard/handler.go:1-73`
- Route registration pattern: `backend/internal/server/routes.go:259-292`
- Most analogous wizards: `backend/internal/wizard/rolebinding.go` (scoped variant), `backend/internal/wizard/hpa.go` (nested CRD)
- Phase 11A cert-manager code: `backend/internal/certmanager/{discovery,types,normalize,handler,poller}.go`
- RBAC pattern: `CanAccessGroupResource` (used throughout Phase 11A handlers)
- Wizard shell: `frontend/components/wizard/WizardStepper.tsx`
- Wizard island reference: `frontend/islands/DeploymentWizard.tsx`
- Cert-manager pages: `frontend/routes/security/certificates/*`, `frontend/islands/{CertificatesList,CertificateDetail,IssuersList}.tsx`

### External

- Certificate CRD reference: https://cert-manager.io/docs/usage/certificate/
- Issuer configuration: https://cert-manager.io/docs/configuration/
- ACME issuer: https://cert-manager.io/docs/configuration/acme/
- API reference v1: https://cert-manager.io/docs/reference/api-docs/

### Related

- Phase 11A: Cert-Manager Observatory (roadmap item #7)
- Sibling plan: `plans/cert-manager-configurable-thresholds.md` (to be written)
- Wizard precedent: 18 existing wizards (see `routes.go:259-292`)
