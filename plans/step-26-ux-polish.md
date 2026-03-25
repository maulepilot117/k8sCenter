# Step 26: UX Polish (Post-Review, 7 Items)

## Overview

Fix broken shortcuts, remove dead code, activate unused components, and add breadcrumbs + clickable owner references. Focused on cleanup and high-value small changes — no new features.

## Review Feedback Applied

- Cut command palette (YAGNI — sidebar already shows all nav items)
- Cut responsive sidebar (desktop-only tool — Monaco, YAML, wide tables)
- Cut recent resources (no home without command palette)
- Re-ordered: dead code removal first, then enhancements
- Added: `SEGMENT_LABELS` map for breadcrumbs, `aria-live` for toast, fallback for unknown owner ref kinds

## Implementation Plan (7 items)

### 1. Clickable Owner References

**File: `frontend/components/k8s/detail/MetadataSection.tsx` lines 34-49**

Owner references show `{ref.kind}/{ref.name}` as plain text. Make them clickable links.

```tsx
import { RESOURCE_DETAIL_PATHS, CLUSTER_SCOPED_KINDS } from "@/lib/constants.ts";

// In ownerReferences rendering:
const kindKey = ref.kind.toLowerCase() + "s"; // ReplicaSet → replicasets
const path = RESOURCE_DETAIL_PATHS[kindKey];
if (path) {
  const href = CLUSTER_SCOPED_KINDS.has(kindKey)
    ? `${path}/${ref.name}`
    : `${path}/${namespace}/${ref.name}`;
  return <a href={href} class="text-brand hover:underline">{ref.kind}/{ref.name}</a>;
}
// Fallback for unknown kinds — render as plain text
return <span>{ref.kind}/{ref.name}</span>;
```

### 2. Fix Keyboard Shortcuts

**File: `frontend/components/ui/SearchBar.tsx`**
- Add `data-search-input` attribute to the input element

**File: `frontend/islands/KeyboardShortcuts.tsx`**
- Remove `g h` from the `shortcuts` display array (never implemented, misleading)

### 3. Toast Consolidation

**Delete: `frontend/components/ui/Toast.tsx`** (per-component `useToast` hook)
**Keep: `frontend/islands/ToastProvider.tsx`** (global `showToast()`)

Migrate callers to `showToast()`:
- `ResourceTable.tsx`
- `ResourceDetail.tsx`
- `SnapshotList.tsx`
- `UserManager.tsx`
- `SettingsPage.tsx`

Add `role="status" aria-live="polite"` to ToastProvider's container div for accessibility.

### 4. Breadcrumb Component

**New file: `frontend/components/ui/Breadcrumb.tsx`**

```tsx
interface BreadcrumbProps {
  items: { label: string; href?: string }[];
}
```

**New constant in `frontend/lib/constants.ts`:**

```ts
export const SEGMENT_LABELS: Record<string, string> = {
  workloads: "Workloads",
  networking: "Networking",
  storage: "Storage",
  config: "Config",
  scaling: "Scaling",
  rbac: "Access Control",
  cluster: "Cluster",
  monitoring: "Monitoring",
  alerting: "Alerting",
  tools: "Tools",
  admin: "Admin",
  settings: "Settings",
  deployments: "Deployments",
  statefulsets: "StatefulSets",
  daemonsets: "DaemonSets",
  pods: "Pods",
  jobs: "Jobs",
  cronjobs: "CronJobs",
  replicasets: "ReplicaSets",
  services: "Services",
  ingresses: "Ingresses",
  endpoints: "Endpoints",
  endpointslices: "EndpointSlices",
  networkpolicies: "Network Policies",
  "cilium-policies": "Cilium Policies",
  flows: "Network Flows",
  cni: "CNI Plugin",
  pvcs: "PVCs",
  pvs: "PersistentVolumes",
  storageclasses: "StorageClasses",
  configmaps: "ConfigMaps",
  secrets: "Secrets",
  serviceaccounts: "ServiceAccounts",
  resourcequotas: "ResourceQuotas",
  limitranges: "LimitRanges",
  hpas: "HPAs",
  pdbs: "PDBs",
  roles: "Roles",
  clusterroles: "ClusterRoles",
  rolebindings: "RoleBindings",
  clusterrolebindings: "ClusterRoleBindings",
  validatingwebhooks: "ValidatingWebhooks",
  mutatingwebhooks: "MutatingWebhooks",
  "yaml-apply": "YAML Apply",
  "storageclass-wizard": "StorageClass Wizard",
  snapshots: "Snapshots",
  overview: "Overview",
  prometheus: "Prometheus",
  dashboards: "Dashboards",
  rules: "Alert Rules",
  general: "General",
  clusters: "Clusters",
  users: "Users",
  auth: "Authentication",
  audit: "Audit Log",
  events: "Events",
  nodes: "Nodes",
  namespaces: "Namespaces",
};
```

Replace ad-hoc breadcrumb in `ResourceDetail.tsx:692-708` with `<Breadcrumb />`.
Add breadcrumbs to list pages via a helper that derives items from `url.pathname`.

### 5. Activate Skeleton + EmptyState Components

**`frontend/components/ui/Skeleton.tsx`** — already built, never used.
**`frontend/components/layout/EmptyState.tsx`** — already built, never used.

Replace inline spinners/empty states in:
- `Dashboard.tsx` — stat cards use Skeleton during loading
- `ResourceTable.tsx` — use TableSkeleton on initial load, EmptyState for empty tables
- `ResourceDetail.tsx` — use Skeleton layout instead of centered spinner

### 6. Error Page Improvements

**File: `frontend/routes/_error.tsx`**

- Add "Retry" button for 500 errors (`location.reload()`)
- Add "Go Back" button (`history.back()`)
- Differentiate 404 (show search suggestion) vs 500 (show retry)

## Acceptance Criteria

- [ ] Owner references are clickable links to parent resources (with fallback for unknown kinds)
- [ ] `/` shortcut focuses the search input
- [ ] `g h` removed from shortcuts help modal
- [ ] Single toast system (`showToast()`), old `useToast` hook deleted
- [ ] Toast container has `aria-live="polite"`
- [ ] Breadcrumb component renders on detail + list pages
- [ ] Dashboard and ResourceTable use Skeleton instead of inline spinner
- [ ] EmptyState component used for empty tables
- [ ] Error page has Retry (500) and Go Back buttons

## Implementation Order

1. Clickable owner references (highest daily value, single file)
2. Fix keyboard shortcuts (2-line fix)
3. Toast consolidation (delete dead code, migrate 5 callers)
4. Breadcrumb component (new component + constant + call site updates)
5. Skeleton + EmptyState activation (use existing components)
6. Error page improvements (retry + go back buttons)

## References

- Owner refs: `frontend/components/k8s/detail/MetadataSection.tsx:34-49`
- Keyboard shortcuts: `frontend/islands/KeyboardShortcuts.tsx`
- SearchBar: `frontend/components/ui/SearchBar.tsx`
- Dead Toast: `frontend/components/ui/Toast.tsx`
- Active ToastProvider: `frontend/islands/ToastProvider.tsx`
- Ad-hoc breadcrumb: `frontend/islands/ResourceDetail.tsx:692-708`
- Unused Skeleton: `frontend/components/ui/Skeleton.tsx`
- Unused EmptyState: `frontend/components/layout/EmptyState.tsx`
- Error page: `frontend/routes/_error.tsx`
- Constants: `frontend/lib/constants.ts`
