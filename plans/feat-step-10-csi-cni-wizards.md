# feat(storage,networking): Step 10 — CSI/CNI Wizards

## Overview

Add storage and networking management capabilities to KubeCenter: a StorageClass creation wizard, CSI driver listing, VolumeSnapshot listing (conditional), CNI auto-detection, and CNI configuration viewing with Cilium-specific editing support.

## Scope Decisions

Based on spec-flow analysis, the following scope decisions resolve the 38 identified gaps:

| Decision | Choice | Rationale |
|----------|--------|-----------|
| CNI editing scope | **Cilium only** for Step 10; Calico/Flannel read-only stubs | Homelab uses Cilium; 3 CNI models is too much scope |
| Cilium config mechanism | **ConfigMap** (`cilium-config` in kube-system) | Always present, no operator dependency; warn about restart |
| ClusterMesh | **Deferred** — read-only status only | Multi-cluster cert exchange is its own feature |
| StorageClass apply | **YAML apply endpoint** (same as existing wizards) | Consistent pattern; Monaco editor available for edits |
| VolumeSnapshot | **Dynamic client** with CRD existence check | No new typed dependencies needed |
| Calico/Flannel editing | **Read-only detection + info** | Stub with "Configuration editing coming soon" |
| StorageClass default annotation | **Include in wizard** as a toggle | Critical operational setting, trivial to add |
| Presets | **Backend-served** via GET endpoint | Keeps frontend thin; allows future dynamic presets |
| Informers | **Add StorageClass + CSIDriver** to InformerManager | Follows "informers for read" architecture principle |

---

## API Contracts

### GET /api/v1/storage/drivers

```json
{
  "data": [
    {
      "name": "driver.longhorn.io",
      "attachRequired": true,
      "podInfoOnMount": true,
      "volumeLifecycleModes": ["Persistent"],
      "storageCapacity": true,
      "fsGroupPolicy": "ReadWriteOnceWithFSType",
      "nodeCount": 3,
      "capabilities": {
        "volumeExpansion": true,
        "snapshot": true,
        "clone": true
      }
    }
  ],
  "metadata": { "total": 1 }
}
```

Capabilities are inferred: `volumeExpansion` from whether any StorageClass using this driver has `allowVolumeExpansion: true` or from the CSIDriver spec; `snapshot` from VolumeSnapshotClass existence for this driver; `clone` from whether the driver supports `CLONE_VOLUME` (heuristic: check VolumeSnapshotClass).

### GET /api/v1/storage/classes

```json
{
  "data": [
    {
      "name": "longhorn",
      "provisioner": "driver.longhorn.io",
      "reclaimPolicy": "Delete",
      "volumeBindingMode": "Immediate",
      "allowVolumeExpansion": true,
      "isDefault": true,
      "parameters": { "numberOfReplicas": "3" },
      "createdAt": "2026-01-15T10:30:00Z"
    }
  ],
  "metadata": { "total": 1 }
}
```

### GET /api/v1/storage/snapshots (conditional — 404 if CRDs absent)

```json
{
  "data": [
    {
      "name": "my-snapshot",
      "namespace": "default",
      "volumeSnapshotClassName": "longhorn-snap",
      "sourcePVC": "my-pvc",
      "readyToUse": true,
      "restoreSize": "10Gi",
      "createdAt": "2026-03-01T08:00:00Z"
    }
  ],
  "metadata": { "total": 1, "available": true }
}
```

### GET /api/v1/storage/presets

```json
{
  "data": {
    "driver.longhorn.io": {
      "displayName": "Longhorn",
      "parameters": {
        "numberOfReplicas": { "default": "3", "description": "Number of replicas", "type": "number" },
        "staleReplicaTimeout": { "default": "2880", "description": "Stale replica timeout (minutes)", "type": "number" },
        "dataLocality": { "default": "disabled", "description": "Data locality", "type": "enum", "options": ["disabled", "best-effort", "strict-local"] }
      }
    },
    "nfs.csi.k8s.io": {
      "displayName": "NFS CSI",
      "parameters": {
        "server": { "default": "", "description": "NFS server address", "type": "string", "required": true },
        "share": { "default": "", "description": "NFS share path", "type": "string", "required": true }
      }
    },
    "ebs.csi.aws.com": {
      "displayName": "AWS EBS",
      "parameters": {
        "type": { "default": "gp3", "description": "Volume type", "type": "enum", "options": ["gp3", "gp2", "io1", "io2", "st1", "sc1"] },
        "encrypted": { "default": "true", "description": "Enable encryption", "type": "boolean" },
        "kmsKeyId": { "default": "", "description": "KMS key ARN (optional)", "type": "string" }
      }
    },
    "rook-ceph.rbd.csi.ceph.com": {
      "displayName": "Rook Ceph RBD",
      "parameters": {
        "clusterID": { "default": "", "description": "Ceph cluster ID", "type": "string", "required": true },
        "pool": { "default": "", "description": "Ceph pool name", "type": "string", "required": true },
        "imageFormat": { "default": "2", "description": "RBD image format", "type": "string" },
        "imageFeatures": { "default": "layering", "description": "RBD image features", "type": "string" }
      }
    }
  }
}
```

### GET /api/v1/networking/cni

```json
{
  "data": {
    "name": "cilium",
    "version": "1.16.4",
    "namespace": "kube-system",
    "daemonSet": "cilium",
    "status": {
      "ready": 3,
      "desired": 3,
      "healthy": true
    },
    "features": {
      "hubble": true,
      "encryption": false,
      "encryptionMode": "",
      "clusterMesh": false,
      "wireguard": false
    },
    "hasCRDs": true,
    "detectionMethod": "daemonset+crd"
  }
}
```

### GET /api/v1/networking/cni/config (Cilium)

```json
{
  "data": {
    "cniType": "cilium",
    "configSource": "configmap",
    "configMapName": "cilium-config",
    "configMapNamespace": "kube-system",
    "editable": true,
    "config": {
      "enable-hubble": "true",
      "hubble-relay": "true",
      "enable-encryption": "false",
      "encryption-type": "",
      "tunnel-protocol": "vxlan",
      "cluster-name": "default",
      "ipam": "cluster-pool",
      "cluster-pool-ipv4-cidr": "10.0.0.0/8",
      "cluster-pool-ipv4-mask-size": "24"
    }
  }
}
```

### PUT /api/v1/networking/cni/config

Request body — partial update of config keys:
```json
{
  "changes": {
    "enable-hubble": "true",
    "hubble-relay": "true"
  },
  "confirmed": true
}
```

The `confirmed: true` flag is required — backend rejects without it (ensures confirmation dialog was shown). Response returns the updated config.

---

## Implementation Plan

### Phase 1: Backend Storage Package

**Files to create:**

```
backend/internal/storage/
├── handler.go          # HTTP handlers for storage endpoints
├── drivers.go          # CSI driver listing + capability enrichment
├── classes.go          # StorageClass listing from informer
├── snapshots.go        # VolumeSnapshot listing (dynamic client, conditional)
└── presets.go          # Driver-specific parameter presets
```

**Tasks:**

1. **Add StorageClass + CSIDriver informers to InformerManager** (`backend/internal/k8s/informers.go`)
   - Add `StorageV1().StorageClasses()` and `StorageV1().CSIDrivers()` informers
   - Update `WaitForSync()` to include new informers
   - Update readiness check
   - Pattern: follow existing `factory.CoreV1().Pods().Informer()` calls

2. **Create `internal/storage/handler.go`** — Handler struct + route registration
   - `Handler` struct with `Informers`, `ClientFactory`, `Logger` fields
   - `RegisterRoutes(r chi.Router)` method
   - Endpoints: `GET /storage/drivers`, `GET /storage/classes`, `GET /storage/snapshots`, `GET /storage/presets`

3. **Create `internal/storage/drivers.go`** — CSI driver listing
   - Read CSIDriver objects from informer cache
   - Enrich with capability detection (check VolumeSnapshotClass existence for snapshot support)
   - Count nodes with driver registered (from CSINode objects if available, else skip)

4. **Create `internal/storage/classes.go`** — StorageClass listing
   - Read from informer cache
   - Include `isDefault` field from `storageclass.kubernetes.io/is-default-class` annotation
   - Standard pagination via existing `paginate[T]` helper

5. **Create `internal/storage/snapshots.go`** — VolumeSnapshot listing (conditional)
   - Check CRD existence via discovery API on handler init
   - If CRDs absent, return `{"data":[], "metadata":{"available":false}}`
   - If present, use dynamic client with impersonation to list
   - Support namespace filtering: `GET /storage/snapshots` (all) and `GET /storage/snapshots/:namespace`

6. **Create `internal/storage/presets.go`** — Hardcoded preset definitions
   - Map of provisioner name → parameter definitions with defaults, descriptions, types
   - Cover: Longhorn, NFS CSI, AWS EBS, Rook Ceph RBD

### Phase 2: Backend Networking Package

**Files to create:**

```
backend/internal/networking/
├── handler.go          # HTTP handlers for networking endpoints
├── detect.go           # CNI auto-detection (DaemonSet scan + CRD check)
├── config.go           # CNI config read/write (ConfigMap-based)
└── cilium.go           # Cilium-specific feature detection and config parsing
```

**Tasks:**

7. **Create `internal/networking/handler.go`** — Handler struct + route registration
   - `Handler` struct with `ClientFactory`, `Logger`, cached detection result
   - Endpoints: `GET /networking/cni`, `GET /networking/cni/config`, `PUT /networking/cni/config`
   - PUT requires `confirmed: true` in body (safety guard)

8. **Create `internal/networking/detect.go`** — CNI detection
   - Scan DaemonSets in `kube-system` (and `cilium`, `calico-system`, `kube-flannel` namespaces)
   - Match by DaemonSet name patterns: `cilium`, `calico-node`, `kube-flannel-ds`
   - Verify with CRD check: `cilium.io`, `crd.projectcalico.org`
   - Extract version from container image tag
   - Check DaemonSet ready vs desired for health status
   - Cache result, re-detect on `GET /networking/cni?refresh=true`

9. **Create `internal/networking/cilium.go`** — Cilium feature detection
   - Read `cilium-config` ConfigMap in kube-system
   - Parse known keys: `enable-hubble`, `enable-encryption`, `encryption-type`, `tunnel-protocol`, `ipam`, etc.
   - Detect features from config values

10. **Create `internal/networking/config.go`** — CNI config read/write
    - Read: parse `cilium-config` ConfigMap into structured response
    - Write: patch ConfigMap with changed keys using impersonating client
    - Audit log all write operations
    - For Calico/Flannel: return config as read-only (editable: false)

### Phase 3: Backend Wizard Extension

11. **Add StorageClass wizard to `internal/wizard/`**
    - Create `storage.go` with `StorageClassInput` struct
    - Fields: name, provisioner, reclaimPolicy, volumeBindingMode, allowVolumeExpansion, isDefault, parameters, mountOptions
    - `Validate()` — DNS subdomain for name (253 chars), valid provisioner, valid reclaim policy enum, valid binding mode enum
    - `ToStorageClass()` — returns `storagev1.StorageClass` typed object
    - Register `POST /api/v1/wizards/storageclass/preview` in wizard routes

### Phase 4: Server Wiring + Helm

12. **Wire storage and networking handlers into server**
    - Add `StorageHandler` and `NetworkingHandler` to `server.Deps`
    - Register routes conditionally in `routes.go`
    - Initialize in `main.go`

13. **Update Helm ClusterRole** (`helm/kubecenter/templates/clusterrole.yaml`)
    - Add `storage.k8s.io` rules: `storageclasses`, `csidrivers`, `csinodes`, `csistoragecapacities` (get, list, watch)
    - Add `snapshot.storage.k8s.io` rules: `volumesnapshots`, `volumesnapshotclasses` (get, list, watch)
    - Add `cilium.io` rules: `ciliumnetworkpolicies`, `ciliumnodes` (get, list) — optional, won't error if CRDs absent

### Phase 5: Frontend — Storage Pages

14. **Add StorageClass list page** (`frontend/routes/storage/storageclasses/index.tsx`)
    - Reuse ResourceTable pattern but with custom columns for StorageClass fields
    - Columns: Name, Provisioner, Reclaim Policy, Binding Mode, Expansion, Default, Age
    - "Create StorageClass" button linking to wizard

15. **Add CSI Drivers page** (`frontend/routes/storage/csi/index.tsx`)
    - Card-based layout showing each driver with capabilities as badges
    - Read-only informational page

16. **Create StorageClass Wizard island** (`frontend/islands/StorageClassWizard.tsx`)
    - 4 steps following DeploymentWizard pattern:
      1. **Basics**: Name, provisioner (dropdown from drivers API), isDefault toggle
      2. **Options**: Reclaim policy (Delete/Retain radio), binding mode (Immediate/WaitForFirstConsumer radio), volume expansion toggle, mount options list
      3. **Parameters**: Key-value editor pre-populated from presets API based on selected provisioner; user can add/remove/edit
      4. **Review**: YAML preview via `POST /api/v1/wizards/storageclass/preview`, apply via YAML apply endpoint
    - `beforeunload` guard, step validation, namespace not needed (cluster-scoped)

17. **Add VolumeSnapshots page** (conditional — `frontend/routes/storage/snapshots/index.tsx`)
    - Check `/api/v1/storage/snapshots` — if `metadata.available: false`, show info message
    - Otherwise render table: Name, Namespace, Source PVC, Class, Ready, Size, Age

### Phase 6: Frontend — Networking Pages

18. **Add CNI Status page** (`frontend/routes/networking/cni/index.tsx`)
    - Renders `CniStatus` island

19. **Create CniStatus island** (`frontend/islands/CniStatus.tsx`)
    - Fetch `GET /api/v1/networking/cni` on mount
    - Show: CNI name + version, DaemonSet health (ready/desired), detected features as badges
    - If Cilium detected and `editable: true`: show config form
    - If Calico/Flannel/unknown: show read-only info + "Configuration editing not yet supported"

20. **Cilium config form within CniStatus**
    - Fetch `GET /api/v1/networking/cni/config`
    - Toggle fields: Hubble, Hubble Relay, Encryption (with mode selector if enabled)
    - Read-only fields: IPAM mode, tunnel protocol, cluster CIDR
    - "Apply Changes" button → confirmation dialog with warning text
    - Warning: "Changing Cilium configuration requires agent restart. Pod-to-pod connectivity may be briefly interrupted (typically 30-60 seconds per node, rolling). Ensure you have console access to your nodes."
    - On confirm: `PUT /api/v1/networking/cni/config` with `confirmed: true`

### Phase 7: Navigation + Integration

21. **Update sidebar navigation** (`frontend/lib/constants.ts`)
    - Add to Storage section: `Storage Classes` → `/storage/storageclasses`, `CSI Drivers` → `/storage/csi`
    - Add `Volume Snapshots` → `/storage/snapshots` (always show; page handles unavailable state)
    - Add to Networking section: `CNI Configuration` → `/networking/cni`
    - Add to Tools section: `StorageClass Wizard` → `/tools/storageclass-wizard`

22. **Add resource icons** (`frontend/components/k8s/ResourceIcon.tsx`)
    - Add icons: `storageclasses`, `csidrivers`, `snapshots`, `cni`

### Phase 8: Tests + Verification

23. **Backend tests**
    - Storage handler tests: driver listing, class listing, snapshot conditional behavior, preset serving
    - Networking handler tests: CNI detection (mock DaemonSets), config read/write, confirmation guard
    - Wizard tests: StorageClassInput validation, ToStorageClass generation
    - Target: 15-20 tests minimum

24. **Smoke test against homelab**
    - Verify CSI driver listing returns Longhorn driver with capabilities
    - Verify StorageClass listing shows existing classes
    - Verify StorageClass wizard creates a working StorageClass
    - Verify CNI detection finds Cilium with version and health
    - Verify Cilium config read shows real config values
    - Verify CNI config PUT with `confirmed: false` is rejected
    - Verify snapshot endpoint returns gracefully (CRDs may/may not exist)

---

## Acceptance Criteria

- [ ] `GET /api/v1/storage/drivers` lists CSI drivers with capabilities
- [ ] `GET /api/v1/storage/classes` lists StorageClasses with default annotation
- [ ] `GET /api/v1/storage/presets` returns parameter presets for known drivers
- [ ] `GET /api/v1/storage/snapshots` returns snapshots or `available: false`
- [ ] `POST /api/v1/wizards/storageclass/preview` generates valid StorageClass YAML
- [ ] StorageClass wizard works end-to-end (create via YAML apply)
- [ ] `GET /api/v1/networking/cni` auto-detects CNI plugin with version and health
- [ ] `GET /api/v1/networking/cni/config` returns Cilium config from ConfigMap
- [ ] `PUT /api/v1/networking/cni/config` updates Cilium ConfigMap with confirmation guard
- [ ] CNI config changes are audit-logged
- [ ] Confirmation dialog with disruption warning shown before CNI apply
- [ ] Calico/Flannel show read-only detection status
- [ ] StorageClass and CSIDriver informers added to InformerManager
- [ ] Helm ClusterRole updated with storage.k8s.io and snapshot.storage.k8s.io permissions
- [ ] Frontend sidebar has Storage Classes, CSI Drivers, Volume Snapshots, CNI Configuration entries
- [ ] All endpoints use impersonation for user-initiated operations
- [ ] Backend tests pass (15+ tests)
- [ ] Smoke tested against homelab k3s cluster

## Dependencies

- Step 7 (YAML apply) — StorageClass wizard uses YAML apply endpoint for final create
- Step 8 (Wizards) — Reuses WizardStepper, WizardReviewStep components and wizard handler pattern
- `k8s.io/client-go` storage/v1 informer factories (already in go.mod)
- Dynamic client for VolumeSnapshot CRDs (already used by YAML handler)

## References

- Existing wizard pattern: `backend/internal/wizard/deployment.go`, `handler.go`
- Frontend wizard pattern: `frontend/islands/DeploymentWizard.tsx`
- Informer setup: `backend/internal/k8s/informers.go`
- Dynamic client cache: `backend/internal/k8s/client.go`
- Monitoring handler (subsystem pattern): `backend/internal/monitoring/handler.go`
- Route registration: `backend/internal/server/routes.go`
- Helm ClusterRole: `helm/kubecenter/templates/clusterrole.yaml`
- CSI driver API: `storage.k8s.io/v1` — CSIDriver, StorageClass, CSINode
- Snapshot API: `snapshot.storage.k8s.io/v1` — VolumeSnapshot, VolumeSnapshotClass
- Cilium config: `cilium-config` ConfigMap in `kube-system`
