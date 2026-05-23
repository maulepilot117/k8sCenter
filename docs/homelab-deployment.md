# Homelab Deployment Guide

Deploy k8sCenter to a k3s ARM64 cluster with MetalLB.

## Prerequisites

- k3s cluster (ARM64) with MetalLB or klipper-lb
- `local-path` StorageClass available
- Docker logged into GHCR: `echo $PAT | docker login ghcr.io -u USERNAME --password-stdin`
- Helm 3.x installed

## Build & Push Images

```bash
# Build ARM64 images and push to GHCR
./scripts/build-push.sh v0.3.2

# Make packages public on GitHub (Settings > Packages > Visibility > Public)
```

## Deploy

```bash
# Create namespace
kubectl create namespace k8scenter

# Install with homelab values
helm install k8scenter ./helm/kubecenter \
  --namespace k8scenter \
  -f ./helm/kubecenter/values-homelab.yaml \
  --set 'postgresql.primary.persistence.storageClass=local-path' \
  --set 'postgresql.image.tag=latest' \
  --set 'postgresql.primary.podSecurityContext.enabled=false' \
  --set 'postgresql.primary.containerSecurityContext.enabled=false'
```

## Access

The frontend gets a LoadBalancer IP (e.g., `10.100.0.16`):

```bash
kubectl get svc -n k8scenter k8scenter-kubecenter-frontend
```

Access at `http://<EXTERNAL-IP>:8000`

## First Login

```bash
# Fetch the auto-generated setup token:
# kubectl get secret -n kubecenter kubecenter -o jsonpath='{.data.setup-token}' | base64 -d

# Create admin account (only needed once — stored in memory, lost on backend restart)
curl -X POST http://<EXTERNAL-IP>:8000/api/v1/setup/init \
  -H "Content-Type: application/json" \
  -H "X-Requested-With: XMLHttpRequest" \
  -d '{"username":"admin","password":"<your-chosen-password>","setupToken":"<from-kubectl-get-secret>"}'
```

Then login at `http://<EXTERNAL-IP>:8000/login` with `admin` / `<your-chosen-password>`.

> **Note:** Admin accounts are in-memory (LocalProvider). They are lost on backend pod restart. The PostgreSQL database stores audit logs, settings, and cluster registrations — not user accounts. User persistence is planned for a future step.

## Upgrade

```bash
# Rebuild images after code changes
./scripts/build-push.sh v0.3.3

# Update tags in values-homelab.yaml, then:
helm upgrade k8scenter ./helm/kubecenter \
  --namespace k8scenter \
  -f ./helm/kubecenter/values-homelab.yaml \
  --set 'postgresql.primary.persistence.storageClass=local-path' \
  --set 'postgresql.image.tag=latest' \
  --set 'postgresql.primary.podSecurityContext.enabled=false' \
  --set 'postgresql.primary.containerSecurityContext.enabled=false'
```

## Teardown

```bash
helm uninstall k8scenter -n k8scenter
kubectl delete pvc -n k8scenter --all
kubectl delete namespace k8scenter
```

## Known Issues

| Issue | Workaround |
|---|---|
| Bitnami PostgreSQL pinned tag missing ARM64 | Use `postgresql.image.tag=latest` |
| NFS StorageClass breaks PostgreSQL | Use `local-path` StorageClass |
| VPN blocks access to LoadBalancer IPs | Disconnect VPN when testing |
| Admin accounts lost on backend restart | Re-run setup/init after restart |
| Rate limit (5 req/min) too aggressive for testing | Set `backend.config.dev=true` in values |
