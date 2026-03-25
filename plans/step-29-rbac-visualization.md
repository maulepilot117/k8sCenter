# Step 29: RBAC Visualization (Post-Review)

## Overview

Add RBAC visualization: filterable relationship table, cross-links between detail pages, effective permissions for any subject. All client-side computation — no new backend endpoints.

## Review Feedback Applied

- Replace tree widget with filterable flat table (DHH — simpler, handles 50+ system bindings)
- Filter `system:` prefixed subjects by default (DHH)
- "Bindings using this Role" must be a new small island, not added to SSR component (Kieran)
- Extract `resolveRoleHref` helper using existing `RESOURCE_DETAIL_PATHS` (DHH, Kieran)
- Add `?limit=500` to all RBAC list fetches (Kieran — pagination correctness)
- Extract effective permissions computation to `lib/rbac-utils.ts` (Kieran — testability)
- Use global `selectedNamespace` signal for namespace scoping
- Document: aggregationRule ClusterRoles show empty rules (known limitation)

## Implementation Plan (3 items)

### 1. Cross-Links Between RBAC Detail Pages

**Ship first — highest value, lowest risk.**

**New island: `frontend/islands/RoleBindingsList.tsx`**

Small island that takes `roleName` and `namespace` props, fetches bindings, and shows which bindings reference this role. Needed because `RoleOverview.tsx` is an SSR component — can't add `useEffect` to it.

```tsx
// Mounted inside ResourceDetail when kind is "roles" or "clusterroles"
export default function RoleBindingsList({ roleName, namespace, clusterScoped }: Props) {
  // Fetch bindings, filter by roleRef.name === roleName
  // Render list of binding names with links + subjects
}
```

**Wire into `ResourceDetail.tsx`** — add a conditional render for role kinds after the overview tab content.

**New helper: `frontend/lib/rbac-utils.ts`**

```typescript
import { RESOURCE_DETAIL_PATHS, CLUSTER_SCOPED_KINDS } from "@/lib/constants.ts";

export function resolveRoleHref(
  roleRefKind: string,
  roleRefName: string,
  namespace?: string,
): string {
  const kindKey = roleRefKind.toLowerCase() + "s"; // ClusterRole → clusterroles
  const path = RESOURCE_DETAIL_PATHS[kindKey];
  if (!path) return "#";
  return CLUSTER_SCOPED_KINDS.has(kindKey)
    ? `${path}/${roleRefName}`
    : `${path}/${namespace}/${roleRefName}`;
}

export interface EffectiveRule {
  resource: string;
  verbs: string[];
  source: string; // "Role: developer (via dev-binding)"
}

export function computeEffectivePermissions(
  subjectName: string,
  bindings: Array<{ metadata: any; roleRef: any; subjects?: any[] }>,
  roles: Map<string, { rules?: any[] }>,
): EffectiveRule[] {
  // Find all bindings where subjectName is in subjects
  // Resolve each binding's roleRef to get rules
  // Return flat array of EffectiveRule with source attribution
}
```

**Modify `RoleBindingOverview.tsx`** — make roleRef name a clickable link using `resolveRoleHref`.

### 2. RBAC Relationship Table

**New route: `frontend/routes/rbac/overview.tsx`**
**New island: `frontend/islands/RBACOverview.tsx`**

Filterable flat table (not a tree) showing Subject → Binding → Role relationships.

**Data flow:**
1. Fetch all 4 RBAC types with `?limit=500` (parallel `Promise.all`)
2. Build subject → bindings → roles map client-side
3. Render as a sortable, filterable table

**Table columns:**
| Subject | Kind | Binding | Role | Namespace | Rules Count |
|---|---|---|---|---|---|
| admin | User | e2e-admin-binding | cluster-admin | (cluster) | 1 (wildcard) |
| system:kube-scheduler | User | system:kube-scheduler | system:kube-scheduler | (cluster) | 12 |

**Filters:**
- Toggle: "Hide system accounts" (on by default — hides `system:` prefixed subjects)
- Namespace selector: uses global `selectedNamespace` signal
- Search box: filters by subject name

**Each cell is a clickable link** to the corresponding detail page.

**Add to nav:** Add "RBAC Overview" to Access Control section in `constants.ts:NAV_SECTIONS`.

### 3. Effective Permissions View

**Within `RBACOverview.tsx`** — click a subject row to expand and show effective permissions.

Uses `computeEffectivePermissions()` from `rbac-utils.ts`:

```
Resource       | Verbs                | Source
pods           | get,list,watch       | Role: developer (via dev-binding in default)
deployments    | get,list,watch,update | Role: developer (via dev-binding in default)
*              | *                    | ClusterRole: cluster-admin (via admin-binding)
```

**Known limitation:** ClusterRoles with `aggregationRule` (e.g., `admin`, `edit`, `view`) have no inline `rules` — they aggregate from child ClusterRoles. The effective permissions table will show empty rules for these. Document in UI: "This role uses aggregation — rules are inherited from child roles."

---

## Acceptance Criteria

- [ ] RoleBindingOverview roleRef is a clickable link to role detail page
- [ ] Role/ClusterRole detail pages show "Bindings using this Role" via new island
- [ ] `/rbac/overview` page shows filterable Subject→Binding→Role table
- [ ] `system:` subjects hidden by default (toggle to show)
- [ ] Clicking a subject row shows effective permissions table with source
- [ ] "RBAC Overview" in Access Control nav section
- [ ] All fetches use `?limit=500`
- [ ] `deno task build` passes

## Implementation Order

1. `rbac-utils.ts` — `resolveRoleHref` + `computeEffectivePermissions`
2. Cross-links: roleRef link in RoleBindingOverview + RoleBindingsList island
3. RBACOverview island + route + nav item
4. Effective permissions expansion within RBACOverview

## References

- RoleOverview: `frontend/components/k8s/detail/RoleOverview.tsx` (SSR, 7 lines)
- RoleBindingOverview: `frontend/components/k8s/detail/RoleBindingOverview.tsx`
- ResourceDetail: `frontend/islands/ResourceDetail.tsx` (host island for detail pages)
- Constants: `frontend/lib/constants.ts` (RESOURCE_DETAIL_PATHS, NAV_SECTIONS)
- Namespace signal: `frontend/lib/namespace.ts` (selectedNamespace)
