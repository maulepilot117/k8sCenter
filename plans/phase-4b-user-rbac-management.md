# Phase 4B: User & RBAC Management — Implementation Plan

## Overview

Phase 4B adds user creation (admin can create new local users with k8s identity + role assignment in one wizard flow) and full RBAC management (create/update/delete for Roles, ClusterRoles, RoleBindings, ClusterRoleBindings with dedicated wizard UIs).

**Branch:** `feat/phase4b-user-rbac-management`
**Depends on:** Phase 4A merged (PR #59) — no code dependencies, just branch base.
**Spec:** `docs/superpowers/specs/2026-03-22-phase4-features-design.md` (Phase 4B section)

---

## Step 4B.1 — User Creation Endpoint

**Backend: `backend/internal/server/handle_users.go`**

Add `handleCreateUser` following the existing handler patterns in the file:

- Route: `POST /api/v1/users` (admin-only, inside existing `/users` group in routes.go)
- Body:
  ```json
  {
    "username": "string",
    "password": "string",
    "k8sUsername": "string (optional, defaults to username)",
    "k8sGroups": ["string array (optional, defaults to [\"system:authenticated\"])"],
    "roles": ["string array (optional, e.g. [\"admin\"])"]
  }
  ```
- Validation:
  - Username: `validUsername` regex (already defined in `handle_setup.go` line 21), max 253 chars
  - Password: 8-128 chars
  - k8sUsername: defaults to username if empty, validated same as username
  - k8sGroups: defaults to `["system:authenticated"]` if empty
  - Uniqueness: `Store().GetByUsername()` returns `ErrUserNotFound` if available
- Implementation: call `s.LocalAuth.CreateUser(ctx, username, password, roles)` (existing method)
  - Then update k8s identity fields via the UserStore if k8sUsername/k8sGroups differ from defaults
- Audit: `ActionCreate`, resource type "User", result success/failure
- Response: 201 Created with `{"username": "...", "k8sUsername": "...", "roles": [...]}` (no password)

**Route registration** in `routes.go` line 113-118, add POST:
```go
ar.Route("/users", func(ur chi.Router) {
    ur.Use(middleware.RequireAdmin)
    ur.Get("/", s.handleListUsers)
    ur.Post("/", s.handleCreateUser)  // NEW
    ur.Delete("/{id}", s.handleDeleteUser)
    ur.Put("/{id}/password", s.handleUpdateUserPassword)
})
```

**Reference patterns:**
- `handle_setup.go:24-96` — existing user creation logic (validation, CreateFirstUser)
- `handle_users.go:108-158` — existing password update handler pattern
- `auth.LocalProvider.CreateUser()` — existing method that handles Argon2id hashing

**Files to modify:**
- `backend/internal/server/handle_users.go` (add handler)
- `backend/internal/server/routes.go` (add POST route)

---

## Step 4B.2 — User Creation + Role Assignment Wizard

**Frontend: `frontend/islands/UserWizard.tsx` (new)**

4-step wizard using WizardStepper:

### Step 1: Account
- Username input (text, validated via regex on blur)
- Debounced uniqueness check: fetch `GET /v1/users`, check if username exists client-side
- Password + Confirm Password inputs (type="password", min 8 chars)
- Validation errors shown inline per field

### Step 2: Kubernetes Identity
- K8s Username input (defaults to local username, editable)
- K8s Groups: tag-style input (reuse KeyValueListEditor pattern but single-value)
  - Default: `["system:authenticated"]`
  - Add/remove group buttons
- Info tooltip: "This identity is used when k8sCenter impersonates the user for Kubernetes API calls. The username and groups determine what RBAC permissions apply."

### Step 3: Role Assignment (optional, skippable)
- Toggle: "Quick assign" vs "Namespace-scoped"
- **Quick assign mode:**
  - Dropdown of existing ClusterRoles fetched from `GET /v1/resources/clusterroles`
  - Creates a ClusterRoleBinding on apply
- **Namespace-scoped mode:**
  - Table of pending assignments, each row:
    - Namespace picker (from `GET /v1/resources/namespaces`)
    - Role/ClusterRole picker (from `GET /v1/resources/roles/{ns}` + `GET /v1/resources/clusterroles`)
    - Remove button
  - "Add Assignment" button adds a new row
  - Creates RoleBinding(s) on apply
- Skip button bypasses role assignment entirely

### Step 4: Review
- Summary table: username, k8s identity, pending role assignments
- YAML preview of RoleBinding/ClusterRoleBinding resources (built client-side, not via preview endpoint — RBAC resources are simple enough)
- Apply flow (sequential):
  1. `POST /v1/users` — create the local user
  2. For each binding: `POST /v1/resources/rolebindings/{ns}` or `POST /v1/resources/clusterrolebindings`
  3. Show per-resource success/failure status
  4. On complete, link back to User Manager page

**Route:** `frontend/routes/settings/users/new.tsx`
**Link from:** Add "Create User" button to `UserManager.tsx` header (next to the page title)

**Files to create/modify:**
- `frontend/islands/UserWizard.tsx` (new)
- `frontend/routes/settings/users/new.tsx` (new)
- `frontend/islands/UserManager.tsx` (add "Create User" button/link)

---

## Step 4B.3 — Full RBAC CRUD Backend

**Backend: Rename `rbac_viewer.go` → `rbac.go`, add create/update/delete**

12 new handlers following the exact pattern from `configmaps.go` / `deployments.go`:

### Namespaced (Role, RoleBinding) — 6 handlers

```go
// Role CRUD
func (h *Handler) HandleCreateRole(w, r)     // POST /resources/roles/{namespace}
func (h *Handler) HandleUpdateRole(w, r)     // PUT /resources/roles/{namespace}/{name}
func (h *Handler) HandleDeleteRole(w, r)     // DELETE /resources/roles/{namespace}/{name}

// RoleBinding CRUD
func (h *Handler) HandleCreateRoleBinding(w, r)   // POST /resources/rolebindings/{namespace}
func (h *Handler) HandleUpdateRoleBinding(w, r)   // PUT /resources/rolebindings/{namespace}/{name}
func (h *Handler) HandleDeleteRoleBinding(w, r)   // DELETE /resources/rolebindings/{namespace}/{name}
```

Each handler:
1. `requireUser(w, r)` — extract authenticated user
2. `chi.URLParam(r, "namespace")` + `chi.URLParam(r, "name")` for update/delete
3. `h.checkAccess(w, r, user, verb, kind, ns)` — RBAC check using `kindRole` / `kindRoleBinding`
4. `decodeBody(w, r, &obj)` for create/update — decode JSON into typed k8s struct (`rbacv1.Role`, `rbacv1.RoleBinding`)
5. `h.impersonatingClient(user)` — get impersonating clientset
6. `cs.RbacV1().Roles(ns).Create/Update/Delete(ctx, ...)` — k8s API call
7. `h.auditWrite(...)` — audit log
8. `writeCreated(w, result)` / `writeData(w, result)` / `w.WriteHeader(204)` — response

### Cluster-scoped (ClusterRole, ClusterRoleBinding) — 6 handlers

Same pattern but no namespace parameter:

```go
func (h *Handler) HandleCreateClusterRole(w, r)          // POST /resources/clusterroles
func (h *Handler) HandleUpdateClusterRole(w, r)          // PUT /resources/clusterroles/{name}
func (h *Handler) HandleDeleteClusterRole(w, r)          // DELETE /resources/clusterroles/{name}

func (h *Handler) HandleCreateClusterRoleBinding(w, r)   // POST /resources/clusterrolebindings
func (h *Handler) HandleUpdateClusterRoleBinding(w, r)   // PUT /resources/clusterrolebindings/{name}
func (h *Handler) HandleDeleteClusterRoleBinding(w, r)   // DELETE /resources/clusterrolebindings/{name}
```

For cluster-scoped: `h.checkAccess(w, r, user, verb, kind, "")` — empty namespace.

### Route registration in `routes.go`

Update the RBAC routes section (currently lines 462-472) to add POST/PUT/DELETE alongside existing GETs:

```go
// RBAC — full CRUD
ar.Get("/resources/roles", h.HandleListRoles)
ar.Get("/resources/roles/{namespace}", h.HandleListRoles)
ar.Get("/resources/roles/{namespace}/{name}", h.HandleGetRole)
ar.Post("/resources/roles/{namespace}", h.HandleCreateRole)
ar.Put("/resources/roles/{namespace}/{name}", h.HandleUpdateRole)
ar.Delete("/resources/roles/{namespace}/{name}", h.HandleDeleteRole)
// ... same pattern for clusterroles, rolebindings, clusterrolebindings
```

**Files to modify:**
- `backend/internal/k8s/resources/rbac_viewer.go` → rename to `rbac.go` (add CRUD handlers)
- `backend/internal/server/routes.go` (add POST/PUT/DELETE routes)

---

## Step 4B.4 — RBAC Wizards + Shared Components

### Shared Component: `frontend/components/wizard/RuleBuilder.tsx` (new)

RBAC PolicyRule editor — used by Role and ClusterRole wizards.

**Props:**
```typescript
interface RuleBuilderProps {
  rules: PolicyRule[];
  onChange: (rules: PolicyRule[]) => void;
}

interface PolicyRule {
  apiGroups: string[];
  resources: string[];
  verbs: string[];
  resourceNames?: string[];
}
```

**UI per rule row:**
- API Groups: multi-select/tags input. Common values: `""` (core), `"apps"`, `"batch"`, `"rbac.authorization.k8s.io"`, `"networking.k8s.io"`. Populate from `GET /v1/cluster/api-resources` if endpoint exists, otherwise use static list.
- Resources: multi-select/tags input. Common values depend on selected API group. Use static presets: pods, deployments, services, configmaps, secrets, etc. Support `"*"` for all.
- Verbs: checkbox group with presets:
  - "Read Only" button → selects get, list, watch
  - "Read/Write" button → selects get, list, watch, create, update, patch, delete
  - "Full" button → selects all + deletecollection
  - Individual checkboxes: get, list, watch, create, update, patch, delete, deletecollection
- Resource Names: optional text input (comma-separated, for restricting to specific named resources)
- Remove rule button (trash icon)

**Footer:** "Add Rule" button appends empty row.

### Shared Component: `frontend/components/wizard/SubjectPicker.tsx` (new)

RBAC Subject selector — used by RoleBinding and ClusterRoleBinding wizards.

**Props:**
```typescript
interface SubjectPickerProps {
  subjects: Subject[];
  onChange: (subjects: Subject[]) => void;
}

interface Subject {
  kind: "User" | "Group" | "ServiceAccount";
  name: string;
  namespace?: string; // only for ServiceAccount
}
```

**UI per subject row:**
- Kind: dropdown (User / Group / ServiceAccount)
- Name: text input. If kind=User, show "Add from local users" button that fetches `GET /v1/users` and shows a dropdown of local usernames.
- Namespace: text input, only visible when kind=ServiceAccount. Namespace picker dropdown.
- Remove button

**Footer:** "Add Subject" button appends empty row.

### `frontend/islands/RoleWizard.tsx` (new)

Used for both Role and ClusterRole creation (prop: `clusterScoped: boolean`).

**Steps:**
1. **Basics** — Name (DNS label validated), Namespace (only for Role, dropdown), Labels (KeyValueListEditor)
2. **Rules** — RuleBuilder component
3. **Review** — YAML preview (build client-side from form state, not via backend preview endpoint)
   - For Role: `apiVersion: rbac.authorization.k8s.io/v1, kind: Role`
   - For ClusterRole: `apiVersion: rbac.authorization.k8s.io/v1, kind: ClusterRole`
   - Apply via `POST /v1/resources/roles/{ns}` or `POST /v1/resources/clusterroles`

**Routes:**
- `frontend/routes/resources/roles/new.tsx` — renders `<RoleWizard clusterScoped={false} />`
- `frontend/routes/resources/clusterroles/new.tsx` — renders `<RoleWizard clusterScoped={true} />`

### `frontend/islands/RoleBindingWizard.tsx` (new)

Used for both RoleBinding and ClusterRoleBinding (prop: `clusterScoped: boolean`).

**Steps:**
1. **Basics** — Name, Namespace (only for RoleBinding)
2. **Role Reference** — Searchable dropdown of existing Roles + ClusterRoles:
   - Fetch `GET /v1/resources/roles/{ns}` (if namespaced) + `GET /v1/resources/clusterroles`
   - Group in dropdown: "Roles (namespace)" section + "ClusterRoles" section
   - Selected role shown as `{kind}: {name}`
3. **Subjects** — SubjectPicker component
4. **Review** — YAML preview + apply via `POST /v1/resources/rolebindings/{ns}` or `POST /v1/resources/clusterrolebindings`

**Routes:**
- `frontend/routes/resources/rolebindings/new.tsx` — renders `<RoleBindingWizard clusterScoped={false} />`
- `frontend/routes/resources/clusterrolebindings/new.tsx` — renders `<RoleBindingWizard clusterScoped={true} />`

### Resource Browser "Create" Buttons

Add `createHref` prop to ResourceTable usage for RBAC pages:
- Roles page: `createHref="/resources/roles/new"`
- ClusterRoles page: `createHref="/resources/clusterroles/new"`
- RoleBindings page: `createHref="/resources/rolebindings/new"`
- ClusterRoleBindings page: `createHref="/resources/clusterrolebindings/new"`

These are passed via the route pages (e.g., `routes/rbac/roles.tsx` or wherever the ResourceTable is rendered for RBAC types).

### Action Buttons (Delete)

Add delete action to RBAC resource types in `frontend/lib/action-handlers.ts`:
- roles, clusterroles, rolebindings, clusterrolebindings should have a "Delete" action
- Follow existing pattern from other resource types (confirm dialog with type-to-confirm for destructive actions)

**Files to create:**
- `frontend/components/wizard/RuleBuilder.tsx` (new)
- `frontend/components/wizard/SubjectPicker.tsx` (new)
- `frontend/islands/RoleWizard.tsx` (new)
- `frontend/islands/RoleBindingWizard.tsx` (new)
- `frontend/islands/UserWizard.tsx` (new — from step 4B.2)
- `frontend/routes/settings/users/new.tsx` (new)
- `frontend/routes/resources/roles/new.tsx` (new)
- `frontend/routes/resources/clusterroles/new.tsx` (new)
- `frontend/routes/resources/rolebindings/new.tsx` (new)
- `frontend/routes/resources/clusterrolebindings/new.tsx` (new)

**Files to modify:**
- `frontend/islands/UserManager.tsx` (add Create User button)
- `frontend/lib/action-handlers.ts` (add delete actions for RBAC types)
- Resource browser route pages for RBAC types (add `createHref`)

---

## Implementation Order

1. **4B.3 first** — RBAC CRUD backend (12 handlers + routes). This unblocks all frontend work.
2. **4B.1 second** — User creation endpoint. Small, independent.
3. **4B.4 third** — Shared components (RuleBuilder, SubjectPicker), then RBAC wizards.
4. **4B.2 last** — User wizard (depends on both user creation endpoint AND RBAC CRUD for role assignment step).

Steps 1+2 (backend) can be done in parallel. Steps 3+4 (frontend) can be done in parallel after backend is ready.

---

## Testing Strategy

- **Backend unit tests**: table-driven tests for each new RBAC handler (create, update, delete) using httptest + fake clientset. Verify RBAC access checks, audit logging, and error handling.
- **User creation tests**: verify uniqueness check, password validation, default k8s identity population.
- **Frontend**: `deno lint`, `deno fmt --check`, `deno check` on all new files.
- **Homelab smoke test**:
  - Create a new local user via wizard, verify login works
  - Create a Role with custom rules, verify YAML is valid
  - Create a ClusterRoleBinding assigning user to cluster-admin, verify impersonation works
  - Delete a RoleBinding, verify it's removed from cluster

---

## Acceptance Criteria

- [ ] `POST /api/v1/users` creates a local user with k8s identity
- [ ] User wizard creates user + assigns role in one flow
- [ ] RBAC CRUD works for all 4 types (12 endpoints)
- [ ] Role wizard builds valid rules with API group/resource/verb selection
- [ ] RoleBinding wizard assigns subjects to roles with local user integration
- [ ] All RBAC operations are audit logged
- [ ] Delete actions available on RBAC resource browser pages
- [ ] Create buttons link to wizards on RBAC resource browser pages
- [ ] All Go tests pass, all frontend lint/type checks pass
- [ ] Homelab smoke test passes
