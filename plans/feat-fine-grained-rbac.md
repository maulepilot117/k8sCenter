# feat: Frontend Permission Gating via Kubernetes RBAC

Expose the user's actual Kubernetes permissions to the frontend so the UI hides actions the user can't perform. No parallel RBAC system — Kubernetes is the single source of truth.

## Problem Statement

Users see buttons they can't use (Scale, Delete, Create) and get confusing 403 errors. The backend already enforces Kubernetes RBAC via impersonation and `SelfSubjectRulesReview`, but the frontend ignores the RBAC data that `/auth/me` already returns.

## Key Insight

The backend already sends RBAC data. The frontend already receives it. It's typed as `unknown` and thrown away at `lib/auth.ts:88`. This feature is largely about **stop ignoring data we already have**.

---

## Implementation Plan

### Phase 1: Type and Expose Existing RBAC Data

**The backend already does this.** `handleAuthMe` calls `RBACChecker.GetSummary()` and returns it as `rbac` in the response. The only backend change needed:

**`backend/internal/auth/rbac.go`** — fix cache key to include groups (pre-existing bug):
```go
// Line 146: cache key should be hash(username + groups), not just username
```

**`backend/internal/server/handle_auth.go`** — add optional `?namespace=` query param to compute permissions for a single namespace instead of all namespaces. When provided, return permissions for only that namespace + cluster-scoped resources (2 API calls instead of N).

**`frontend/lib/k8s-types.ts`** — type the existing `rbac` field properly instead of `unknown`:

```typescript
export interface RBACSummary {
  clusterScoped: ResourcePermissions;
  namespaces: Record<string, ResourcePermissions>;
}

export interface ResourcePermissions {
  [kind: string]: string[]; // verbs
}
```

### Phase 2: Frontend Permission Utilities

**`frontend/lib/permissions.ts`** — create permission checking hook:

```typescript
export function usePermissions(): {
  canPerform: (kind: string, verb: string, namespace: string) => boolean;
  isAdmin: boolean;  // computed: user.roles.includes("admin")
};
```

**`frontend/lib/auth.ts`** — stop ignoring the `rbac` field:
- Type the `/auth/me` response properly (it already returns `rbac`)
- Store RBAC summary in a signal alongside the user signal
- Re-fetch triggers: token refresh, namespace change, tab focus, after 403 response

### Phase 3: Gate Frontend UI

**`frontend/islands/ResourceTable.tsx`:**
- Filter `ACTIONS_BY_KIND` based on `canPerform(kind, verb, selectedNamespace)`
- Hide action buttons the user lacks permission for
- Hide "Create" button if user lacks `create` verb for that kind

**`frontend/lib/action-handlers.ts`:**
- Add `getVisibleActions(kind, namespace, permissions)` filter function

**`frontend/islands/Sidebar.tsx`:**
- Hide "Users", "Audit Log", "Authentication" sections if `!isAdmin`
- Resource sections stay visible (user may have partial access)

**Error recovery:** When any API call returns 403, invalidate cached permissions and re-fetch `/auth/me`. This is the self-correcting mechanism for stale caches.

---

## What This Does NOT Do (And Why)

- **No custom role definitions** — k8s ClusterRoles are the role system
- **No database tables** — k8s is the single source of truth
- **No role editor UI** — use kubectl, Helm, or k8sCenter's existing YAML apply
- **No OIDC/LDAP role mapping** — providers already map to k8s groups via config
- **No `isAdmin` backend field** — computed on frontend: `user.roles.includes("admin")`
- **No app-level permission middleware** — `RequireAdmin` stays for admin endpoints, k8s RBAC handles resources

## Important: Admin Role for Non-Local Users

OIDC and LDAP users are **never** app-level admins by default. Their `Roles` field is `["user"]`. To make an OIDC/LDAP user an admin, a cluster administrator must either:
1. Extend the OIDC/LDAP provider config to map a specific claim/group to `admin` (future work)
2. Create a local admin account for them

This is by design — the `admin` app role controls non-k8s features (user management, audit logs, settings) and should not be grantable via external identity providers without explicit configuration.

## Edge Cases

1. **60-second cache staleness** — UI permissions may be up to 60s stale. Backend still enforces on every request. Frontend auto-corrects after 403 + re-fetch.
2. **Cluster-scoped resources** — nodes, namespaces, PVs use cluster-scoped rules review (no namespace filter).
3. **Many namespaces** — `/auth/me?namespace=X` computes for selected namespace only. Full summary deferred to explicit request.
4. **No permissions at all** — empty resource tables, no action buttons. Clean UX, no confusing 403s.
5. **Admin with limited k8s RBAC** — rare but valid. App admin sections visible, but k8s resource actions gated by actual k8s permissions.

## Acceptance Criteria

- [ ] `rbac` field in `/auth/me` properly typed (not `unknown`)
- [ ] Optional `?namespace=` param on `/auth/me` for single-namespace permissions
- [ ] RBAC cache key includes groups (fix pre-existing bug)
- [ ] `usePermissions()` hook with `canPerform(kind, verb, namespace)`
- [ ] ResourceTable hides actions user can't perform
- [ ] "Create" buttons hidden when user lacks `create` verb
- [ ] Sidebar hides admin sections for non-admin users
- [ ] 403 response triggers permission re-fetch
- [ ] No new database tables
- [ ] `go test ./... -race` passes
- [ ] `deno lint && deno fmt --check && deno task build` pass

## Files to Create/Modify

| File | Action | Purpose |
|------|--------|---------|
| `backend/internal/auth/rbac.go` | Modify | Fix cache key to include groups |
| `backend/internal/server/handle_auth.go` | Modify | Add ?namespace= param |
| `frontend/lib/k8s-types.ts` | Modify | Type RBACSummary properly |
| `frontend/lib/permissions.ts` | Create | Permission checking hook |
| `frontend/lib/auth.ts` | Modify | Store RBAC data, add re-fetch triggers |
| `frontend/islands/ResourceTable.tsx` | Modify | Filter actions by permissions |
| `frontend/lib/action-handlers.ts` | Modify | Add getVisibleActions filter |
| `frontend/islands/Sidebar.tsx` | Modify | Hide admin sections |
