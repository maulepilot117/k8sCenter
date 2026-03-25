# Step 25: Production Hardening (Post-Review)

## Overview

Harden k8sCenter for production: fix NetworkPolicy security gap, Go runtime tuning, container scanning, probe fixes, CI hardening. 10 items after plan review (cut SBOM, PDB, CI Gate wrapper).

## Review Feedback Applied

- Cut SBOM generation (no consumers, YAGNI)
- Deferred PDB (replicaCount=1, renders to nothing)
- Removed CI Gate wrapper job (over-engineered for solo repo)
- Added NetworkPolicy wildcard fix (Security HIGH)
- Added frontend startup probe
- Changed startupProbe from /healthz to /readyz
- GOMEMLIMIT as values.yaml field (not hardcoded bytes)
- Keep busybox init container (defer removal until retry logic added)
- Trivy: scan amd64 locally before multi-arch push

---

## Implementation Plan (10 items)

### 1. Fix NetworkPolicy Wildcard Ingress (Security HIGH)

**File: `helm/kubecenter/templates/networkpolicy.yaml`**

When `ingress.enabled=true`, backend ingress allows ANY pod in ANY namespace. Fix by scoping to ingress controller namespace:

```yaml
# Replace the broad namespaceSelector: {} with:
- from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: {{ .Values.networkPolicy.ingressNamespace | default "ingress-nginx" }}
```

Add `networkPolicy.ingressNamespace` to values.yaml.

### 2. Go Runtime Tuning

**File: `backend/cmd/kubecenter/main.go`**
- Add `import _ "go.uber.org/automaxprocs"`

**File: `backend/go.mod`**
- Add `go.uber.org/automaxprocs` dependency

**File: `helm/kubecenter/values.yaml`**
- Add `backend.goMemLimit: "230MiB"` (90% of 256Mi limit)

**File: `helm/kubecenter/templates/deployment-backend.yaml`**
- Add env var: `GOMEMLIMIT: {{ .Values.backend.goMemLimit | default "230MiB" }}`

### 3. Database Pool Defaults

**File: `backend/internal/config/defaults.go`**
```go
const (
    DefaultDatabaseMaxConns = 10
    DefaultDatabaseMinConns = 2
)
```

**File: `backend/internal/config/config.go`**
Wire defaults into Load() function.

### 4. Probe Tuning

**File: `helm/kubecenter/templates/deployment-backend.yaml`**

```yaml
startupProbe:
  httpGet: { path: /readyz, port: http }  # was /healthz — need /readyz for informer sync
  initialDelaySeconds: 2
  periodSeconds: 3
  failureThreshold: 20
  timeoutSeconds: 3

livenessProbe:
  httpGet: { path: /healthz, port: http }
  periodSeconds: 15
  failureThreshold: 3
  timeoutSeconds: 5

readinessProbe:
  httpGet: { path: /readyz, port: http }
  periodSeconds: 10
  failureThreshold: 3
  successThreshold: 1
  timeoutSeconds: 5
```

**File: `helm/kubecenter/templates/deployment-frontend.yaml`**

Add startup probe + fix liveness:

```yaml
startupProbe:
  tcpSocket: { port: http }
  initialDelaySeconds: 2
  periodSeconds: 3
  failureThreshold: 20
  timeoutSeconds: 3

livenessProbe:
  tcpSocket: { port: http }       # was httpGet /  — don't depend on backend
  periodSeconds: 15
  failureThreshold: 3

readinessProbe:
  httpGet: { path: /, port: http }
  periodSeconds: 10
  failureThreshold: 3
  timeoutSeconds: 5
```

### 5. terminationGracePeriodSeconds

**File: `helm/kubecenter/templates/deployment-backend.yaml`**

Add `terminationGracePeriodSeconds: 45` to pod spec.

### 6. CI Permission Hardening

**File: `.github/workflows/ci.yml`**

Change from workflow-level to per-job permissions:

```yaml
permissions: {}  # deny by default

jobs:
  changes:
    permissions: { contents: read }
  backend:
    permissions: { contents: read }
  frontend:
    permissions: { contents: read }
  version-tag:
    permissions: { contents: write }
  build-backend-image:
    permissions: { contents: read, packages: write, security-events: write }
  build-frontend-image:
    permissions: { contents: read, packages: write, security-events: write }
```

### 7. Trivy Container Scanning

**File: `.github/workflows/ci.yml`**

For each image build job, split into: build (no push, load local) → Trivy scan → push multi-arch.

Since `load: true` doesn't work with multi-platform, scan the amd64 variant locally:

```yaml
# Step 1: Build amd64-only for scanning
- uses: docker/build-push-action@v6
  with:
    context: backend
    file: backend/Dockerfile
    platforms: linux/amd64
    load: true
    push: false
    tags: k8scenter-backend:scan

# Step 2: Scan
- uses: aquasecurity/trivy-action@0.33.1
  with:
    image-ref: k8scenter-backend:scan
    format: sarif
    output: trivy-backend.sarif
    severity: CRITICAL,HIGH
    ignore-unfixed: true
    exit-code: '1'

# Step 3: Upload SARIF (even if scan failed)
- uses: github/codeql-action/upload-sarif@v4
  if: always()
  with:
    sarif_file: trivy-backend.sarif

# Step 4: Multi-arch build + push (only if scan passed)
- uses: docker/build-push-action@v6
  with:
    platforms: linux/arm64,linux/amd64
    push: true
    tags: ...
```

### 8. values.yaml Updates

Add new fields:
```yaml
backend:
  goMemLimit: "230MiB"

networkPolicy:
  ingressNamespace: "ingress-nginx"
```

### 9. Keep Init Container (Deferred)

Keep busybox init container for now. The pgx pool does not retry initial connections by default — removing the init container would cause crash-loops when PostgreSQL starts slowly. Add a TODO comment noting this should be replaced with Go-level retry logic in a future step.

### 10. E2E Informational Check

Already runs on PRs and main pushes. No changes needed — it's already informational. Making it required is deferred until stability is confirmed.

---

## Acceptance Criteria

- [ ] NetworkPolicy scoped to ingress namespace when ingress enabled
- [ ] `automaxprocs` imported in backend main.go
- [ ] GOMEMLIMIT configurable via values.yaml
- [ ] DB pool defaults: maxconns=10, minconns=2
- [ ] Backend startupProbe uses /readyz (not /healthz)
- [ ] All probes have timeoutSeconds and failureThreshold
- [ ] Frontend liveness uses tcpSocket (not HTTP /)
- [ ] Frontend has startupProbe
- [ ] terminationGracePeriodSeconds: 45
- [ ] CI permissions deny-by-default, per-job minimum
- [ ] Trivy scans amd64 image before GHCR push
- [ ] Trivy SARIF uploaded to GitHub Security tab
- [ ] Helm lint passes
- [ ] Homelab deployment succeeds

## Implementation Order

1. NetworkPolicy fix (security first)
2. Go runtime tuning (automaxprocs + GOMEMLIMIT)
3. DB pool defaults
4. Probe tuning (all probes + frontend startup)
5. Frontend liveness fix
6. terminationGracePeriodSeconds
7. CI permission hardening
8. Trivy scanning
9. Homelab smoke test
