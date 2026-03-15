# Step 13: Helm Chart — Production Hardening

## Overview

Expand the existing skeleton Helm chart (backend deployment, service, serviceaccount, clusterrole/binding) into a production-ready chart with frontend deployment, ingress, secrets management, network policies, PDB, PVC for future SQLite (Step 14), and helm tests. Add `values.schema.json` for input validation.

## Problem Statement / Motivation

The current chart deploys only the Go backend. Production use requires:
- Frontend deployment + service (Deno/Fresh on port 8000)
- Ingress for external access with TLS
- Secrets management (JWT secret, setup token, OIDC client secrets, LDAP bind passwords)
- Application ConfigMap for non-secret config
- NetworkPolicy restricting pod-to-pod traffic
- PodDisruptionBudget for zero-downtime upgrades
- PVC for audit log persistence (used by Step 14)
- Helm test hooks for post-install validation
- `values.schema.json` for CI/CD validation of values files

## Proposed Solution

Extend `helm/kubecenter/` with new templates and an expanded `values.yaml` covering all 12 completed steps' configuration surfaces.

## Technical Considerations

- **Single replica constraint**: SQLite (Step 14) requires `ReadWriteOnce` PVC, so `replicaCount` must be 1 when persistence is enabled. Document this.
- **Secret auto-generation**: JWT secret should be auto-generated on first install via a pre-install hook or `lookup` function. Must survive `helm upgrade`.
- **Frontend BACKEND_URL**: The frontend container needs the backend's in-cluster service URL as `BACKEND_URL` env var.
- **Ingress**: Support nginx and Traefik ingress classes. TLS via cert-manager annotations.
- **NetworkPolicy**: Backend needs egress to k8s API, Prometheus, Grafana, LDAP servers. Frontend needs egress only to backend.
- **ARM64 support**: Homelab is ARM64 (k3s). Both container images must be multi-arch or ARM64.

## Existing Chart (from Step 1)

```
helm/kubecenter/
├── Chart.yaml                          # v0.1.0, kube-prometheus-stack subchart
├── values.yaml                         # Backend-only config
├── templates/
│   ├── _helpers.tpl                    # name, fullname, labels, selectorLabels, serviceAccountName
│   ├── deployment-backend.yaml         # Backend pod spec (distroless, non-root, probes)
│   ├── service-backend.yaml            # ClusterIP service
│   ├── serviceaccount.yaml             # SA with annotations
│   ├── clusterrole.yaml                # Read + impersonate permissions
│   ├── clusterrolebinding.yaml         # SA → ClusterRole binding
│   └── monitoring/
│       ├── grafana-config-cm.yaml      # Grafana sidecar config
│       └── grafana-dashboards-cm.yaml  # Pre-built dashboards
└── charts/
    └── kube-prometheus-stack-82.10.3.tgz  # Subchart (conditional)
```

## Files to Create

```
helm/kubecenter/
├── values.schema.json                      # JSON Schema for values validation
├── templates/
│   ├── deployment-frontend.yaml            # Deno/Fresh frontend pod
│   ├── service-frontend.yaml               # Frontend ClusterIP service
│   ├── ingress.yaml                        # Ingress with optional TLS
│   ├── configmap-app.yaml                  # Non-secret app config
│   ├── secret-app.yaml                     # JWT secret, setup token, OIDC/LDAP secrets
│   ├── networkpolicy.yaml                  # Ingress/egress restrictions
│   ├── poddisruptionbudget.yaml            # PDB for backend
│   ├── pvc-data.yaml                       # PVC for SQLite audit logs (Step 14)
│   └── tests/
│       └── test-connection.yaml            # Helm test: curl /healthz + /readyz
```

## Files to Modify

- `values.yaml` — Expand with frontend, auth, alerting, OIDC/LDAP, persistence, networkPolicy sections
- `Chart.yaml` — Bump version to 0.2.0
- `deployment-backend.yaml` — Add secret env var refs (JWT, setup token, OIDC, LDAP, alerting), optional PVC mount
- `_helpers.tpl` — Add helper for frontend fullname

## Expanded values.yaml Structure

```yaml
replicaCount: 1

backend:
  image:
    repository: kubecenter-backend
    tag: ""
    pullPolicy: IfNotPresent
  port: 8080
  resources:
    requests: { cpu: 100m, memory: 128Mi }
    limits: { cpu: 500m, memory: 256Mi }
  config:
    dev: false
    logLevel: info
    logFormat: json
    clusterID: local

frontend:
  enabled: true
  image:
    repository: kubecenter-frontend
    tag: ""
    pullPolicy: IfNotPresent
  port: 8000
  resources:
    requests: { cpu: 50m, memory: 64Mi }
    limits: { cpu: 200m, memory: 128Mi }

serviceAccount:
  create: true
  name: ""
  annotations: {}

service:
  type: ClusterIP
  port: 8080
  frontendPort: 8000

ingress:
  enabled: false
  className: ""
  annotations: {}
  hosts:
    - host: k8scenter.local
      paths:
        - path: /
          pathType: Prefix
  tls: []

auth:
  jwtSecret: ""         # Auto-generated if empty
  setupToken: ""         # Optional: for automated initial setup
  oidc: []               # Array of OIDC provider configs
  ldap: []               # Array of LDAP provider configs

alerting:
  enabled: false
  webhookToken: ""       # Auto-generated if empty
  retentionDays: 30
  rateLimit: 120
  recipients: []
  smtp:
    host: ""
    port: 587
    username: ""
    password: ""
    from: ""
    tlsInsecure: false

monitoring:
  deploy: false
  namespace: ""
  prometheus:
    url: ""
  grafana:
    url: ""
    apiToken: ""

cors:
  allowedOrigins: []     # Auto-set from ingress host if empty

persistence:
  enabled: false         # Enable for SQLite audit logs (Step 14)
  storageClass: ""
  size: 1Gi
  accessMode: ReadWriteOnce

networkPolicy:
  enabled: true

podDisruptionBudget:
  enabled: true
  minAvailable: 0        # Allow full disruption for single-replica

monitoring-stack:        # Subchart overrides (unchanged from Step 1)
  ...
```

## Acceptance Criteria

- [ ] `helm lint helm/kubecenter` passes
- [ ] `helm template` renders all manifests without errors
- [ ] `helm install` in a kind/k3s cluster deploys both backend and frontend
- [ ] Frontend connects to backend via in-cluster service URL
- [ ] Ingress routes traffic to frontend (when enabled)
- [ ] JWT secret auto-generated on install, survives upgrades
- [ ] NetworkPolicy restricts traffic: frontend→backend only, backend→k8s API + monitoring
- [ ] PDB configured (even for single replica, to document intent)
- [ ] `helm test` validates /healthz and /readyz
- [ ] Pod security: non-root (65534), read-only rootfs, drop ALL capabilities, seccomp RuntimeDefault
- [ ] `values.schema.json` validates required fields
- [ ] All env vars from Steps 1-12 are configurable via values.yaml
- [ ] ClusterRole has explicit resource lists (no wildcards) — already done in Step 1

## Dependencies & Risks

- **Container images**: Backend and frontend Docker images must be built and pushed to a registry before `helm install` works. For local testing, use `kind load docker-image`.
- **Ingress controller**: Ingress template requires an ingress controller in the cluster. k3s includes Traefik by default.
- **PVC**: Persistence requires a StorageClass. k3s provides `local-path` by default.

## References

- Existing chart: `helm/kubecenter/`
- Backend Dockerfile: `backend/Dockerfile` (distroless, UID 65534, port 8080)
- Frontend Dockerfile: `frontend/Dockerfile` (deno, port 8000, needs BACKEND_URL env)
- CLAUDE.md security checklist: Container images run as non-root, no shell, NetworkPolicy, TLS
- Helm best practices: https://helm.sh/docs/chart_best_practices/
