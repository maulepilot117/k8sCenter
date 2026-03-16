# Step 19: Complete Resource Coverage

## Overview

Add all remaining core Kubernetes resource types to k8sCenter. Currently 19 types are supported. This step adds 12 more types covering autoscaling, storage volumes, quotas, service accounts, webhooks, and CRDs — bringing the total to 31.

## Current Coverage (19 types)

Workloads: Pods, Deployments, StatefulSets, DaemonSets, Jobs, CronJobs
Networking: Services, Ingresses, NetworkPolicies
Config: ConfigMaps, Secrets
Storage: PersistentVolumeClaims
Cluster: Namespaces, Nodes, Events
RBAC: Roles, ClusterRoles, RoleBindings, ClusterRoleBindings

## New Types to Add (12)

| Resource | API Group | Scope | Priority | Detail View |
|---|---|---|---|---|
| ReplicaSets | apps/v1 | Namespaced | High | Basic (usually managed by Deployments) |
| Endpoints | v1 | Namespaced | High | Shows pod IPs behind Services |
| HorizontalPodAutoscalers | autoscaling/v2 | Namespaced | High | Yes — metrics, targets, scaling history |
| PersistentVolumes | v1 | Cluster | High | Yes — capacity, status, bound PVC |
| StorageClasses | storage.k8s.io/v1 | Cluster | High | Yes — provisioner, parameters |
| ResourceQuotas | v1 | Namespaced | Medium | Yes — used vs hard limits |
| LimitRanges | v1 | Namespaced | Medium | Yes — default limits per container |
| ServiceAccounts | v1 | Namespaced | Medium | Basic |
| PodDisruptionBudgets | policy/v1 | Namespaced | Medium | Yes — minAvailable, status |
| EndpointSlices | discovery.k8s.io/v1 | Namespaced | Medium | Basic |
| ValidatingWebhookConfigurations | admissionregistration.k8s.io/v1 | Cluster | Low | Basic |
| MutatingWebhookConfigurations | admissionregistration.k8s.io/v1 | Cluster | Low | Basic |

## Implementation Per Type

For each type, add:

### Backend
1. **Informer registration** in `k8s/informers.go` — add to the resource specs list
2. **CRUD handlers** in `k8s/resources/` — new file or extend existing (List, Get, Create, Update, Delete)
3. **Route registration** in `server/routes.go` — register handler methods
4. **ClusterRole** in Helm — add resource to the read permissions (if not already covered)

### Frontend
1. **Column definitions** in `lib/resource-columns.ts` — display columns for table view
2. **Type definitions** in `lib/k8s-types.ts` — TypeScript interfaces
3. **Resource routes** — browse pages under appropriate sidebar sections
4. **Detail view components** in `components/k8s/detail/` — for types with detail views
5. **Resource icons** in `components/k8s/ResourceIcon.tsx`
6. **Sidebar nav entries** in `lib/constants.ts`

## Pattern to Follow

Each type follows the exact same pattern as existing types. Example for PersistentVolumes:

**Backend** (`resources/pvs.go`):
```go
func (h *Handler) HandleListPVs(w http.ResponseWriter, r *http.Request) { ... }
func (h *Handler) HandleGetPV(w http.ResponseWriter, r *http.Request) { ... }
```

**Frontend** (`resource-columns.ts`):
```typescript
const pvColumns: Column<K8sResource>[] = [
  nameColumn, { key: "capacity", ... }, { key: "status", ... }, ageColumn,
];
```

## Sidebar Organization Update

```
Cluster:     + PersistentVolumes, StorageClasses
Workloads:   + ReplicaSets
Networking:  + Endpoints, EndpointSlices
Config:      + ServiceAccounts, ResourceQuotas, LimitRanges
Scaling:     (new section) HorizontalPodAutoscalers, PodDisruptionBudgets
Admin:       (new section) Webhooks (Validating + Mutating)
```

## Acceptance Criteria

- [ ] All 12 new resource types browsable in the resource table
- [ ] List, Get for all new types (Create/Update/Delete for applicable types)
- [ ] Detail views for HPA, PV, StorageClass, ResourceQuota, LimitRange, PDB
- [ ] Column definitions with status badges for all types
- [ ] Informers registered for all types that need real-time updates
- [ ] Sidebar sections updated with new entries
- [ ] ClusterRole updated in Helm chart
- [ ] `make test` passes

## References

- Existing resource handler pattern: `backend/internal/k8s/resources/deployments.go`
- Existing column pattern: `frontend/lib/resource-columns.ts`
- Existing detail view pattern: `frontend/components/k8s/detail/DeploymentOverview.tsx`
- Phase 2 plan: `plans/phase-2-production-multi-cluster.md` (Step 19)
