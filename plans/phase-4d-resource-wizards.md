# Phase 4D: Resource Wizards — Implementation Plan

## Overview

Phase 4D adds 10 resource creation wizards, a generic wizard preview handler (retrofitting all existing wizards), a shared ContainerForm component, and a shared ContainerInput backend struct. All 17 wizard preview endpoints use a single `WizardInput` interface.

**Branch:** `feat/phase4d-resource-wizards`
**Depends on:** Phase 4C merged
**Spec:** `plans/phase-4-features-and-wizards.md` (Phase 4D section)

### Plan Review Changes (from reviewer feedback)

- Generic `handlePreview` retrofits ALL existing 7 handlers (consistent codebase, not split old/new)
- Secret wizard preview shows UNMASKED values (user's own data, masking is harmful UX)
- CronScheduleInput inlined in CronJob wizard (only 2 consumers, extract later if needed)
- Added RestartPolicy to Job/CronJob inputs (k8s rejects Always for Jobs)
- Added VolumeMounts to ContainerInput (StatefulSet needs to mount VCTs)
- Workload wizards auto-generate `app: {name}` labels for selectors
- DaemonSet collapsed from 3 steps to 2 (nodeSelector alongside container config)
- NetworkPolicy simplified: no ipBlock in v1, cap at 5 ingress + 5 egress rules, TCP/UDP only
- Ship in 3 PRs for manageable review and independent homelab smoke tests
- Cut CiliumNetworkPolicy wizard (already deferred) and Namespace wizard (already exists)

### Design Decisions

- **2-step wizards** (Configure + Review) for: ConfigMap, Secret, Ingress, DaemonSet, HPA, PDB.
- **3-step wizards** for: Job, CronJob, StatefulSet, NetworkPolicy.
- **Generic `handlePreview`** with `WizardInput` interface — ALL 17 preview endpoints use it.
- **Shared `ContainerInput`** (backend) and `ContainerForm` (frontend) — 5 consumers.
- **Auto-generated labels** — all workload wizards set `app: {name}` on metadata/selector/template.

---

## Batch 1: Infrastructure + Simple Wizards (PR 1)

### Step 4D.0 — Generic Preview Handler + Retrofit

**Backend: `backend/internal/wizard/handler.go`**

```go
type WizardInput interface {
    Validate() []FieldError
    ToYAML() (string, error)
}

func (h *Handler) handlePreview(newInput func() WizardInput) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if _, ok := httputil.RequireUser(w, r); !ok { return }
        input := newInput()
        if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(input); err != nil {
            httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
            return
        }
        if errs := input.Validate(); len(errs) > 0 { writeValidationErrors(w, errs); return }
        yaml, err := input.ToYAML()
        if err != nil { ... }
        httputil.WriteData(w, map[string]string{"yaml": yaml})
    }
}
```

**Retrofit existing wizard inputs** — add `ToYAML() (string, error)` method to:
- `DeploymentInput` (calls `ToDeployment()` + marshal)
- `ServiceInput` (calls `ToService()` + marshal)
- `StorageClassInput` (calls `ToStorageClass()` + marshal)
- `RoleBindingInput` (calls `ToRoleBinding/ToClusterRoleBinding` based on ClusterScope + marshal)
- `PVCInput` (calls `ToPersistentVolumeClaim()` + marshal)
- `SnapshotInput` (calls `ToVolumeSnapshot()` + marshal `.Object`)
- `ScheduledSnapshotInput` (already has `ToMultiDocYAML()` — alias it)

**Delete** individual `Handle*Preview` methods. Replace route registration:
```go
wr.Post("/deployment/preview", h.handlePreview(func() WizardInput { return &DeploymentInput{} }))
wr.Post("/service/preview", h.handlePreview(func() WizardInput { return &ServiceInput{} }))
// ... all 17 endpoints as one-liners
```

Run all existing tests to verify no regressions.

### Step 4D.1 — ConfigMap Wizard

**Backend: `backend/internal/wizard/configmap.go`**
- `ConfigMapInput`: Name, Namespace, Data (map[string]string)
- Validate: DNS label, data keys valid, total size < 1MB
- ToYAML: marshal `corev1.ConfigMap`

**Frontend: `frontend/islands/ConfigMapWizard.tsx`** — 2-step (Configure + Review)
**Route:** `frontend/routes/config/configmaps/new.tsx`

### Step 4D.2 — Secret Wizard

**Backend: `backend/internal/wizard/secret.go`**
- `SecretInput`: Name, Namespace, Type, Data
- Validate: DNS label, valid type enum, type-specific keys
- ToYAML: marshal `corev1.Secret` with `stringData` — **values UNMASKED**

**Frontend: `frontend/islands/SecretWizard.tsx`** — 2-step (Configure + Review)
- Dynamic data form per type: Opaque (key-value), TLS (cert+key), BasicAuth (username+password), DockerConfigJSON (registry/username/password/email)
**Route:** `frontend/routes/config/secrets/new.tsx`

### Step 4D.3 — Ingress Wizard

**Backend: `backend/internal/wizard/ingress.go`**
- `IngressInput`: Name, Namespace, IngressClassName, Rules[], TLS[]
- Validate: DNS label, at least one rule, paths start with `/`, valid pathType, port 1-65535
- ToYAML: marshal `networkingv1.Ingress`

**Frontend: `frontend/islands/IngressWizard.tsx`** — 2-step (Configure + Review)
- Host/path table + TLS toggle
**Route:** `frontend/routes/networking/ingresses/new.tsx`

---

## Batch 2: Workload Wizards (PR 2)

### Step 4D.4 — ContainerForm + ContainerInput Extraction

**Backend: `backend/internal/wizard/container.go` (new)**
```go
type ContainerInput struct {
    Image, Command[], Args[], Env[], Ports[], Resources, Probes, VolumeMounts[]
}
func (c *ContainerInput) Validate(prefix string) []FieldError
func (c *ContainerInput) ToContainer(name string) corev1.Container
```

Refactor `DeploymentInput` to embed `ContainerInput`.

**Frontend: `frontend/components/wizard/ContainerForm.tsx` (new)**
Extract from DeploymentWizard steps. Refactor DeploymentWizard to use it.

### Step 4D.5 — Job Wizard

**Backend: `backend/internal/wizard/job.go`**
- `JobInput`: Name, Namespace, Container, **RestartPolicy** (Never/OnFailure, default Never), Completions, Parallelism, BackoffLimit, ActiveDeadlineSeconds
- Auto-generate `app: {name}` labels

**Frontend: `frontend/islands/JobWizard.tsx`** — 3-step (Basics + Container + Review)
**Route:** `frontend/routes/workloads/jobs/new.tsx`

### Step 4D.6 — CronJob Wizard

**Backend: `backend/internal/wizard/cronjob.go`**
- `CronJobInput`: Name, Namespace, Schedule, Container, **RestartPolicy**, ConcurrencyPolicy, HistoryLimits, Suspend

**Frontend: `frontend/islands/CronJobWizard.tsx`** — 3-step (Basics+Schedule + Container + Review)
- Inline cron presets (not shared component)
**Route:** `frontend/routes/workloads/cronjobs/new.tsx`

### Step 4D.7 — DaemonSet Wizard

**Backend: `backend/internal/wizard/daemonset.go`**
- `DaemonSetInput`: Name, Namespace, Container, NodeSelector, MaxUnavailable

**Frontend: `frontend/islands/DaemonSetWizard.tsx`** — 2-step (Configure + Review)
- ContainerForm + nodeSelector + maxUnavailable all on one page
**Route:** `frontend/routes/workloads/daemonsets/new.tsx`

### Step 4D.8 — StatefulSet Wizard

**Backend: `backend/internal/wizard/statefulset.go`**
- `StatefulSetInput`: Name, Namespace, ServiceName, Replicas, Container (with **VolumeMounts**), VolumeClaimTemplates[], PodManagementPolicy

**Frontend: `frontend/islands/StatefulSetWizard.tsx`** — 3-step (Basics + Container+Volumes + Review)
**Route:** `frontend/routes/workloads/statefulsets/new.tsx`

---

## Batch 3: Advanced + Integration (PR 3)

### Step 4D.9 — NetworkPolicy Wizard (simplified v1)

**Backend: `backend/internal/wizard/networkpolicy.go`**
- `NetworkPolicyInput`: Name, Namespace, PodSelector, PolicyTypes, IngressRules[], EgressRules[]
- Each rule: Ports[] + From/To peers (podSelector, namespaceSelector only — **no ipBlock in v1**)
- Cap: max 5 ingress + 5 egress rules, TCP/UDP only

**Frontend: `frontend/islands/NetworkPolicyWizard.tsx`** — 3-step (Basics+PodSelector + Rules + Review)
**Route:** `frontend/routes/networking/networkpolicies/new.tsx`

### Step 4D.10 — HPA + PDB Wizards

**HPA Backend: `backend/internal/wizard/hpa.go`**
- `HPAInput`: Name, Namespace, TargetKind (Deployment/StatefulSet), TargetName, MinReplicas, MaxReplicas, CPUTarget%, MemoryTarget%

**HPA Frontend: `frontend/islands/HPAWizard.tsx`** — 2-step (Configure + Review)
**Route:** `frontend/routes/scaling/hpas/new.tsx`

**PDB Backend: `backend/internal/wizard/pdb.go`**
- `PDBInput`: Name, Namespace, Selector, MinAvailable OR MaxUnavailable

**PDB Frontend: `frontend/islands/PDBWizard.tsx`** — 2-step (Configure + Review)
**Route:** `frontend/routes/scaling/pdbs/new.tsx`

### Step 4D.11 — Frontend Integration

- Add `createHref` to all 10 resource table pages
- Verify all existing wizards still work after generic handler retrofit + ContainerForm extraction
- Final homelab smoke test

---

## Files Summary

### New files (~31)
**Backend (11):** container.go, configmap.go, secret.go, ingress.go, job.go, cronjob.go, daemonset.go, statefulset.go, networkpolicy.go, hpa.go, pdb.go
**Frontend islands (10):** ConfigMap, Secret, Ingress, Job, CronJob, DaemonSet, StatefulSet, NetworkPolicy, HPA, PDB
**Frontend routes (10):** config/configmaps/new, config/secrets/new, networking/ingresses/new, networking/networkpolicies/new, workloads/jobs/new, workloads/cronjobs/new, workloads/daemonsets/new, workloads/statefulsets/new, scaling/hpas/new, scaling/pdbs/new
**Shared components (1):** ContainerForm.tsx

### Modified files (~5)
- `backend/internal/wizard/handler.go` (generic handler, delete old methods)
- `backend/internal/wizard/deployment.go` (embed ContainerInput, add ToYAML)
- `backend/internal/server/routes.go` (refactor to one-liner registrations)
- `frontend/islands/DeploymentWizard.tsx` (use ContainerForm)
- All existing wizard input types (add ToYAML method)

---

## Testing Strategy

- **Backend:** `*_test.go` per wizard. Table-driven Validate() + ToYAML() tests.
- **ContainerInput:** shared validation tests (empty image, invalid port, probes, volume mounts).
- **Generic handler:** handler-level test with mock WizardInput verifying decode/validate/respond cycle.
- **Retrofit verification:** run all existing wizard tests after handler refactor.
- **Frontend:** deno lint + fmt + check on all new files.
- **Homelab smoke test per PR.**

---

## Acceptance Criteria

- [ ] Generic `handlePreview` replaces ALL 17 preview handlers (7 existing + 10 new)
- [ ] All existing wizard tests pass after retrofit
- [ ] ContainerForm shared across Deployment, Job, CronJob, DaemonSet, StatefulSet
- [ ] Job/CronJob set RestartPolicy (Never/OnFailure), reject Always
- [ ] StatefulSet containers reference VolumeMounts for VCTs
- [ ] All workload wizards auto-generate `app: {name}` selector labels
- [ ] Secret preview shows unmasked stringData values
- [ ] NetworkPolicy v1: no ipBlock, max 5 ingress + 5 egress rules, TCP/UDP only
- [ ] HPA targets Deployment/StatefulSet with CPU/memory % targets
- [ ] PDB sets minAvailable OR maxUnavailable (mutually exclusive)
- [ ] All 10 new wizards produce valid YAML that applies successfully
- [ ] All `createHref` buttons visible on resource browser pages
- [ ] All Go tests pass, all frontend lint/type checks pass
- [ ] Homelab smoke test passes for each PR
