# Step 27: Grafana Dashboard Provisioning (Post-Review, 4 Items)

## Overview

Fix broken dashboard references, populate Helm ConfigMap stubs, and add provision-once logic. Scope reduced from 6 to 4 items after review.

## Review Feedback Applied

- Cut iframe embedding (new-tab behavior is fine for dashboard gallery)
- Cut version-based provisioning skip (premature optimization — 7 API calls/5min is negligible)
- Cut namespace-overview + storage-health dashboards (no UI surface routes to them yet)
- Added provision-once flag instead of version checks (simpler)
- Added Makefile diff check for JSON drift

## Implementation Plan (4 items)

### 1. Add Missing Dashboard JSONs

**New files in `backend/internal/monitoring/dashboards/`:**

**`statefulset_detail.json`** — UID: `kubecenter-statefulset-detail`
- Template vars: `$namespace`, `$statefulset`
- Panels: Ready/Desired Replicas (gauge), Pod CPU (timeseries), Pod Memory (timeseries), Network I/O (timeseries), Ready Replicas over time (timeseries)

**`daemonset_detail.json`** — UID: `kubecenter-daemonset-detail`
- Template vars: `$namespace`, `$daemonset`
- Panels: Ready/Desired Nodes (gauge), Pod CPU (timeseries), Pod Memory (timeseries), Misscheduled (stat)

Follow existing schema: schemaVersion 39, `kubecenter` tag, 30s refresh, 1h default.

### 2. Populate Helm ConfigMap with .Files.Glob

Copy all 7 dashboard JSONs to `helm/kubecenter/dashboards/`.

Replace the stub ConfigMap template:

```yaml
{{- if .Values.monitoring.deploy }}
{{- range $path, $_ := .Files.Glob "dashboards/*.json" }}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "kubecenter.fullname" $ }}-dashboard-{{ trimSuffix ".json" (base $path) }}
  labels:
    grafana_dashboard: "1"
data:
  {{ base $path }}: |-
{{ $.Files.Get $path | indent 4 }}
{{- end }}
{{- end }}
```

Add Makefile target to check for drift:
```makefile
check-dashboards:
	diff backend/internal/monitoring/dashboards/ helm/kubecenter/dashboards/ --exclude=embed.go
```

### 3. Update ResourceDashboardMap

**File: `backend/internal/monitoring/metrics.go`**

Verify existing entries match the new JSONs. The map already has `statefulsets` and `daemonsets` entries — just need to confirm UIDs match.

### 4. Provision-Once Flag in Discovery

**File: `backend/internal/monitoring/discovery.go`**

Add `dashboardsProvisioned bool` to the `Discoverer` struct. Only provision when:
- Grafana was just discovered (first time or after being unavailable)
- `dashboardsProvisioned` is false

```go
if grafClient != nil && !d.dashboardsProvisioned {
    count, err := grafClient.ProvisionDashboards()
    if err == nil {
        d.dashboardsProvisioned = true
    }
}
// Reset flag if Grafana becomes unavailable
if grafClient == nil {
    d.dashboardsProvisioned = false
}
```

## Acceptance Criteria

- [ ] 7 dashboard JSONs in `backend/internal/monitoring/dashboards/`
- [ ] Same 7 JSONs in `helm/kubecenter/dashboards/`
- [ ] Helm ConfigMap uses `.Files.Glob` (no empty stubs)
- [ ] Dashboards provisioned once on discovery, not every 5 min
- [ ] `make check-dashboards` passes (no drift)
- [ ] `helm lint` passes
- [ ] `go vet` passes

## References

- Dashboard JSONs: `backend/internal/monitoring/dashboards/`
- Grafana client: `backend/internal/monitoring/grafana.go`
- Discovery: `backend/internal/monitoring/discovery.go`
- ResourceDashboardMap: `backend/internal/monitoring/metrics.go:77-87`
- Helm ConfigMap: `helm/kubecenter/templates/monitoring/grafana-dashboards-cm.yaml`
