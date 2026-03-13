# feat(detail): Resource Detail Views with Tabs

## Overview

Add detail pages for all 18 resource types. Users click a row in the ResourceTable to navigate to a detail page showing Overview, YAML, Events, and Metrics (placeholder) tabs. Detail pages support deep-linking, live WebSocket updates, and URL-hash-based tab state.

## Problem Statement / Motivation

Step 5 delivered resource list pages with real-time updates. Users can see all resources but cannot inspect individual resources without `kubectl describe`. Detail views are the core interaction for understanding resource state, debugging issues, and preparing for actions (scale, delete, restart) that will be wired in Step 8.

## Design Decisions

These decisions resolve the gaps identified during spec analysis:

| Decision | Choice | Rationale |
|---|---|---|
| Island data fetching | Client-side only (no SSR fetch) | `lib/api.ts` is client-only; consistent with Dashboard/ResourceTable pattern |
| Island prop interface | `kind`, `name`, `namespace?`, `clusterScoped`, `title` as strings/booleans | Serializable, simple, all the island needs to fetch and subscribe |
| Island architecture | Single `ResourceDetail` island + per-kind overview components | Avoids 18 island files; each kind gets a `components/k8s/detail/` component for Overview tab |
| Tab state | URL hash (`#yaml`, `#events`, `#metrics`), `history.replaceState` | Supports deep-linking without polluting browser history |
| Delete-while-viewing | Persistent "deleted" banner, last-known state stays visible, "Back to list" link | User keeps context; less jarring than redirect |
| 404 handling | Inline error inside island (not `_error.tsx`) | Layout (sidebar/topbar) stays visible for navigation |
| Namespace selector on detail page | Ignored; namespace comes from URL params | Detail page is bound to a specific resource; changing selector navigates to list |
| Sidebar active state | `currentPath.startsWith(item.href)` | One-line fix; detail pages highlight parent list item |
| WS for secrets detail | Disabled (`enableWS={false}`) | Secrets excluded from informer cache; same as list |
| YAML managed fields | Stripped by default; "Show managed fields" toggle | Matches `kubectl` default; managedFields adds hundreds of noisy lines |
| YAML library | `npm:yaml` (v2) | Smaller than js-yaml, better TypeScript types, schema-aware |
| YAML highlighting | CSS classes on `<pre>` with hand-tokenized spans | No external dependency; YAML structure is simple enough (keys, strings, numbers, comments) |
| Events filtering | Client-side filter on `involvedObject.kind` + `involvedObject.name` | No backend changes needed; events list already exists |
| Events kind mapping | New `RESOURCE_API_KINDS` map: `deployments` -> `Deployment`, etc. | Required for `involvedObject.kind` matching (PascalCase vs lowercase plural) |
| Events for cluster-scoped | Fetch from all namespaces, filter by name | Node events may be in any namespace |
| Secret reveal | Inline replacement of `****`, auto-hide after 30s, base64-decoded display | Simple, secure, no modal needed |
| Metrics tab | Visible but disabled with "Coming soon" message | Sets user expectation for Step 9 |
| Resource actions | Deferred to Step 8, except read-only display | No delete/scale/restart buttons in Step 6 |
| YAML auto-update on WS event | "Resource updated" banner; user clicks to refresh YAML | Prevents jarring content shifts while reading |
| Row click navigation | `<a>` link on name column + `onRowClick` on entire row | Accessible (keyboard, right-click) + convenient |
| Loading state | Show tab chrome immediately; spinner inside active tab panel | User can orient themselves while data loads |
| Browser tab title | Set via `document.title` in island: `"{name} - {Kind} - KubeCenter"` | Better UX for multiple open tabs |

## Technical Approach

### Architecture

```
Route file (SSR shell)          Island (client-side)              Backend
routes/.../[namespace]/         islands/                          GET /api/v1/resources/
  [name].tsx                    ResourceDetail.tsx                  :kind/:ns/:name
  - extracts params             - fetches resource via REST
  - renders island              - subscribes to WS (kind+ns)     GET /api/v1/resources/
    with string props           - filters WS events by UID         events/:ns
  - passes kind,                - manages tab state (hash)
    namespace, name             - renders per-kind Overview       [new] GET /api/v1/resources/
                                  via components/k8s/detail/        rolebindings/:ns/:name
                                - renders YAML tab                [new] GET /api/v1/resources/
                                - renders Events tab                clusterrolebindings/:name
                                - renders Metrics placeholder
```

### Implementation Phases

#### Phase 1: Foundation (backend fixes + shared components)

**Backend: Add missing Get handlers**

The backend is missing `HandleGetRoleBinding` and `HandleGetClusterRoleBinding`. Add these to `rbac_viewer.go` following the existing `HandleGetRole` / `HandleGetClusterRole` pattern, and register them in `routes.go`.

Files:
- `backend/internal/k8s/resources/rbac_viewer.go` — add `HandleGetRoleBinding`, `HandleGetClusterRoleBinding`
- `backend/internal/server/routes.go` — register GET routes for rolebindings, clusterrolebindings

**Frontend: Shared utilities and components**

Extract `age()` helper from `resource-columns.ts` to `lib/format.ts` (shared between list and detail).

Add `RESOURCE_API_KINDS` mapping to `lib/constants.ts`:
```typescript
// lib/constants.ts
export const RESOURCE_API_KINDS: Record<string, string> = {
  pods: "Pod",
  deployments: "Deployment",
  statefulsets: "StatefulSet",
  daemonsets: "DaemonSet",
  services: "Service",
  ingresses: "Ingress",
  configmaps: "ConfigMap",
  secrets: "Secret",
  namespaces: "Namespace",
  nodes: "Node",
  persistentvolumeclaims: "PersistentVolumeClaim",
  jobs: "Job",
  cronjobs: "CronJob",
  networkpolicies: "NetworkPolicy",
  roles: "Role",
  clusterroles: "ClusterRole",
  rolebindings: "RoleBinding",
  clusterrolebindings: "ClusterRoleBinding",
  events: "Event",
};
```

Add `RESOURCE_DETAIL_PATHS` mapping (kind -> URL section prefix) to `lib/constants.ts`:
```typescript
export const RESOURCE_DETAIL_PATHS: Record<string, string> = {
  pods: "/workloads/pods",
  deployments: "/workloads/deployments",
  statefulsets: "/workloads/statefulsets",
  daemonsets: "/workloads/daemonsets",
  jobs: "/workloads/jobs",
  cronjobs: "/workloads/cronjobs",
  services: "/networking/services",
  ingresses: "/networking/ingresses",
  networkpolicies: "/networking/networkpolicies",
  persistentvolumeclaims: "/storage/pvcs",
  pvcs: "/storage/pvcs",
  configmaps: "/config/configmaps",
  secrets: "/config/secrets",
  nodes: "/cluster/nodes",
  namespaces: "/cluster/namespaces",
  events: "/cluster/events",
  roles: "/rbac/roles",
  clusterroles: "/rbac/clusterroles",
  rolebindings: "/rbac/rolebindings",
  clusterrolebindings: "/rbac/clusterrolebindings",
};
```

Add `npm:yaml` to `deno.json` imports for JSON-to-YAML conversion.

Create `components/ui/Tabs.tsx` — reusable accessible tab component:
- `role="tablist"`, `role="tab"`, `role="tabpanel"` ARIA attributes
- Arrow key navigation between tabs
- Active/inactive Tailwind styling matching project conventions
- Lazy mount: tab panel content rendered only when first activated, kept mounted after
- Props: `tabs: { key: string, label: string, disabled?: boolean }[]`, `activeTab: string`, `onTabChange: (key: string) => void`, `children` keyed by tab key

Create `components/ui/CodeBlock.tsx` — read-only syntax-highlighted code display:
- YAML syntax highlighting via CSS classes (key, string, number, boolean, null, comment)
- Copy-to-clipboard button with "Copied!" feedback (2s timeout)
- Line numbers optional
- Tailwind-styled monospace `<pre>` with dark mode support
- No external highlighting library dependency

Fix `Sidebar.tsx` active state: change `currentPath === item.href` to `currentPath === item.href || currentPath.startsWith(item.href + "/")`.

#### Phase 2: Core Detail Island

Create `islands/ResourceDetail.tsx` — the main detail island:

**Props interface:**
```typescript
interface ResourceDetailProps {
  kind: string;          // API kind string: "deployments", "pods", etc.
  name: string;          // Resource name from URL
  namespace?: string;    // Namespace from URL (undefined for cluster-scoped)
  clusterScoped?: boolean;
  title: string;         // Display title: "Deployment", "Pod", etc.
}
```

**Signal structure:**
```typescript
const resource = useSignal<K8sResource | null>(null);
const loading = useSignal(true);
const error = useSignal<string | null>(null);
const deleted = useSignal(false);
const activeTab = useSignal("overview"); // from URL hash on mount
```

**Data fetching pattern (follows ResourceTable):**
1. On mount, read `window.location.hash` to set initial `activeTab`
2. Subscribe to WS (`subscribe(subId, kind, namespace, callback)`)
3. In WS callback: filter by `resource.metadata.uid`, handle `EVENT_MODIFIED` (update signal), `EVENT_DELETED` (set `deleted = true`), `EVENT_RESYNC` (re-fetch)
4. Fetch resource via `apiGet` after subscribing
5. On 404: set `error = "not found"`, do NOT throw HttpError
6. On 403: set `error = "permission denied"`
7. Cleanup: unsubscribe WS on unmount

**Tab rendering:**
- Overview: render per-kind overview component based on `kind` prop
- YAML: lazy-mount, convert `resource` JSON to YAML (strip `managedFields`), render in `CodeBlock`
- Events: lazy-mount, fetch events on first activation, filter by `involvedObject`
- Metrics: disabled tab with placeholder

**Header layout:**
```
[ResourceIcon] [Kind] / [namespace] / [name]      [age] [status badge]
[breadcrumb: Kind > namespace > name]
```

**Deleted state banner:**
```
[yellow banner] This [kind] was deleted. [Back to {kind} list]
```

#### Phase 3: Per-Kind Overview Components

Create `components/k8s/detail/` directory with one component per resource type. Each component receives the typed resource as a prop and renders its Overview tab content using `Card` components for sections.

**File manifest:**

| File | Key sections |
|---|---|
| `DeploymentOverview.tsx` | Replicas (desired/ready/available/updated), strategy, conditions, container images, selector |
| `PodOverview.tsx` | Phase badge, conditions, per-container status (image, state, restarts, ports, resources), volumes, node |
| `ServiceOverview.tsx` | Type, clusterIP, ports, selector, externalIP/loadBalancer |
| `NodeOverview.tsx` | Conditions, capacity vs allocatable, taints, addresses, system info, labels |
| `StatefulSetOverview.tsx` | Replicas, update strategy, pod management policy, volume claim templates |
| `DaemonSetOverview.tsx` | Desired/current/ready, update strategy, node selector, tolerations |
| `IngressOverview.tsx` | Ingress class, rules (host/path/backend), TLS, default backend |
| `ConfigMapOverview.tsx` | Data keys with content preview (truncated), immutable flag |
| `SecretOverview.tsx` | Type, data keys with masked values, per-key reveal button (30s auto-hide) |
| `NamespaceOverview.tsx` | Phase, labels, annotations, resource quotas (if present) |
| `PVCOverview.tsx` | Phase, capacity, storage class, access modes, volume name |
| `JobOverview.tsx` | Completions, parallelism, duration, conditions, backoff limit |
| `CronJobOverview.tsx` | Schedule, last/next run, suspend, concurrency policy, history limits |
| `NetworkPolicyOverview.tsx` | Pod selector, ingress rules, egress rules, policy types |
| `RoleOverview.tsx` | Rules table (apiGroups, resources, verbs) |
| `ClusterRoleOverview.tsx` | Rules table, aggregation rules if present |
| `RoleBindingOverview.tsx` | Subjects table, roleRef |
| `ClusterRoleBindingOverview.tsx` | Subjects table, roleRef |

**Shared sub-components** (in `components/k8s/detail/`):
- `ConditionsTable.tsx` — Renders k8s conditions array as table (type, status, reason, message, lastTransitionTime)
- `MetadataSection.tsx` — Labels, annotations, ownerReferences, creation timestamp, UID
- `KeyValueTable.tsx` — Generic two-column table for labels, annotations, env vars, etc.

#### Phase 4: Route Files and Navigation Wiring

**Create 18 detail route files.** Each is a thin ~10-line file:

Namespaced resources (14 files) — pattern:
```typescript
// routes/workloads/deployments/[namespace]/[name].tsx
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default function DeploymentDetailPage(props: { params: { namespace: string; name: string } }) {
  return (
    <ResourceDetail
      kind="deployments"
      title="Deployment"
      name={props.params.name}
      namespace={props.params.namespace}
    />
  );
}
```

Cluster-scoped resources (4 files) — pattern:
```typescript
// routes/cluster/nodes/[name].tsx
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default function NodeDetailPage(props: { params: { name: string } }) {
  return (
    <ResourceDetail
      kind="nodes"
      title="Node"
      name={props.params.name}
      clusterScoped
    />
  );
}
```

**Full route file manifest:**

| Resource | Route file path |
|---|---|
| deployments | `routes/workloads/deployments/[namespace]/[name].tsx` |
| statefulsets | `routes/workloads/statefulsets/[namespace]/[name].tsx` |
| daemonsets | `routes/workloads/daemonsets/[namespace]/[name].tsx` |
| pods | `routes/workloads/pods/[namespace]/[name].tsx` |
| jobs | `routes/workloads/jobs/[namespace]/[name].tsx` |
| cronjobs | `routes/workloads/cronjobs/[namespace]/[name].tsx` |
| services | `routes/networking/services/[namespace]/[name].tsx` |
| ingresses | `routes/networking/ingresses/[namespace]/[name].tsx` |
| networkpolicies | `routes/networking/networkpolicies/[namespace]/[name].tsx` |
| pvcs | `routes/storage/pvcs/[namespace]/[name].tsx` |
| configmaps | `routes/config/configmaps/[namespace]/[name].tsx` |
| secrets | `routes/config/secrets/[namespace]/[name].tsx` |
| roles | `routes/rbac/roles/[namespace]/[name].tsx` |
| rolebindings | `routes/rbac/rolebindings/[namespace]/[name].tsx` |
| nodes | `routes/cluster/nodes/[name].tsx` |
| namespaces | `routes/cluster/namespaces/[name].tsx` |
| clusterroles | `routes/rbac/clusterroles/[name].tsx` |
| clusterrolebindings | `routes/rbac/clusterrolebindings/[name].tsx` |

**Wire up ResourceTable row click navigation:**

In `ResourceTable.tsx`, add `onRowClick` to `DataTable` that navigates to the detail URL. Also make the name column a clickable `<a>` link (accessible, supports right-click open-in-new-tab).

The detail URL is constructed using `RESOURCE_DETAIL_PATHS[kind]`:
- Namespaced: `${RESOURCE_DETAIL_PATHS[kind]}/${item.metadata.namespace}/${item.metadata.name}`
- Cluster-scoped: `${RESOURCE_DETAIL_PATHS[kind]}/${item.metadata.name}`

**Wire up namespace selector behavior on detail pages:**

In `ResourceDetail.tsx`, add a `useEffect` that watches `selectedNamespace.value`. When it changes while on a detail page, navigate to the list page: `globalThis.location.href = RESOURCE_DETAIL_PATHS[kind]`.

## Acceptance Criteria

### Functional
- [ ] All 18 resource types have working detail pages accessible via row click
- [ ] Detail pages load correctly via direct URL (deep link)
- [ ] Overview tab shows resource-specific information with status badges
- [ ] YAML tab shows syntax-highlighted YAML with managedFields stripped by default
- [ ] YAML tab has copy-to-clipboard with "Copied!" feedback
- [ ] YAML tab has "Show managed fields" toggle
- [ ] Events tab shows filtered events for the specific resource
- [ ] Metrics tab shows disabled placeholder
- [ ] Tab state persists in URL hash (e.g., `#yaml`)
- [ ] WebSocket events update the detail view in real-time (MODIFIED updates data, DELETED shows banner)
- [ ] Sidebar highlights the parent list item while on a detail page
- [ ] Name column in ResourceTable is a clickable link to detail page
- [ ] Secret detail page shows masked values with per-key reveal (auto-hide 30s)
- [ ] 404 shows inline "not found" error (layout stays visible)
- [ ] 403 shows inline "permission denied" error
- [ ] Namespace selector change on detail page navigates to list page
- [ ] Browser tab title shows resource name and kind

### Non-Functional
- [ ] `go vet ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `deno lint` and `deno fmt --check` pass
- [ ] Tabs are accessible (ARIA roles, keyboard arrow navigation)
- [ ] No new external dependencies except `npm:yaml`
- [ ] Smoke tested against homelab k3s cluster

## Dependencies & Risks

**Dependencies:**
- Step 5 complete (ResourceTable, WS, resource list pages) -- done
- Backend Get handlers for all 18 types -- RoleBinding and ClusterRoleBinding are missing (fix in Phase 1)
- `npm:yaml` added to `deno.json` imports

**Risks:**
- 18 overview components are repetitive work; mitigate with shared sub-components (ConditionsTable, MetadataSection, KeyValueTable)
- YAML tab without Monaco editor (Step 7) means read-only only; this is intentional for Step 6
- Events filtering by involvedObject requires the kind mapping; wrong mapping = empty Events tab
- Secret reveal endpoint returns base64-encoded data; frontend must decode before display

## References & Research

### Internal References
- List page pattern: `frontend/routes/workloads/deployments.tsx`
- ResourceTable WS pattern: `frontend/islands/ResourceTable.tsx:104-152`
- DataTable onRowClick: `frontend/components/ui/DataTable.tsx` (prop exists, unused)
- Status colors: `frontend/lib/status-colors.ts`
- K8s types: `frontend/lib/k8s-types.ts`
- Backend Get handler pattern: `backend/internal/k8s/resources/deployments.go:HandleGetDeployment`
- RBAC viewer (missing gets): `backend/internal/k8s/resources/rbac_viewer.go`
- Route registration: `backend/internal/server/routes.go`
- Secret reveal: `backend/internal/k8s/resources/secrets.go:HandleRevealSecret`
- PR #5: Step 5 resource browser with WS

### External References
- [WAI-ARIA Tabs Pattern](https://www.w3.org/WAI/ARIA/apg/patterns/tabs/)
- [Fresh 2.x Dynamic Routes](https://fresh.deno.dev/docs/concepts/routing)
- [npm:yaml v2 docs](https://eemeli.org/yaml/)
- K8s dashboards studied: Kubernetes Dashboard, Lens, Headlamp, Rancher
