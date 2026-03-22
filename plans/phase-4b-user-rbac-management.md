# Phase 4B: User & RBAC Management — Implementation Plan

## Overview

Phase 4B adds user creation (admin can create new local users with k8s identity) and RBAC binding management (create/update/delete for RoleBindings and ClusterRoleBindings with a wizard UI). Roles and ClusterRoles remain read-only — custom roles are an infrastructure-as-code concern, not a GUI workflow.

**Branch:** `feat/phase4b-user-rbac-management`
**Depends on:** Phase 4A merged (PR #59)
**Spec:** `docs/superpowers/specs/2026-03-22-phase4-features-design.md` (Phase 4B section)

### Plan Review Changes (from reviewer feedback)

- Simplified User wizard from 4 steps to 2 (Account + Review, no integrated role assignment)
- Cut Role/ClusterRole CRUD (6 handlers) — keep read-only viewer, only add binding CRUD
- Cut RuleBuilder component (zero consumers)
- Inline SubjectPicker in RoleBindingWizard (1 consumer, not shared)
- Block `system:` prefixed k8s identities (security)
- Single atomic user creation (extend CreateUser, not two-step)
- Use backend preview endpoint for YAML (consistency with wizard pipeline)
- Kill debounced uniqueness check (let backend 409)
- ~700 LOC saved vs original plan

---

## Step 4B.1 — User Creation Endpoint

**Backend: `backend/internal/server/handle_users.go`**

Add `handleCreateUser`:
- Route: `POST /api/v1/users` (admin-only, inside existing `/users` group)
- Body:
  ```json
  {
    "username": "string",
    "password": "string",
    "k8sUsername": "string (optional, defaults to username)",
    "k8sGroups": ["optional, defaults to [\"system:authenticated\"]"],
    "roles": ["optional, e.g. [\"admin\"]"]
  }
  ```
- Validation:
  - Username: `validUsername` regex (handle_setup.go:21), max 253 chars
  - Password: 8-128 chars
  - k8sUsername: defaults to username if empty. **Reject `system:` prefix** — k8s reserves this for built-in identities. Validated same regex as username.
  - k8sGroups: defaults to `["system:authenticated"]`. **Reject `system:masters`** — this bypasses all RBAC and should never be assigned via GUI.
  - `http.MaxBytesReader` for body size limit (match existing handlers)
- Implementation: **Single atomic write** — extend `LocalAuth.CreateUser()` or add `CreateUserWithIdentity()` that accepts k8sUsername + k8sGroups parameters. Do NOT create-then-update in two steps.
- Audit: `ActionCreate`, resource type "User", result success/failure
- Response: 201 Created with `{"username": "...", "k8sUsername": "...", "roles": [...]}`
- Duplicate username: return 409 Conflict (no client-side uniqueness check needed)

**Route registration** in `routes.go`, add POST to existing `/users` group:
```go
ur.Post("/", s.handleCreateUser)
```

**Files to modify:**
- `backend/internal/server/handle_users.go` (add handler)
- `backend/internal/server/routes.go` (add POST route)
- `backend/internal/auth/local.go` (extend CreateUser to accept k8s identity params)

---

## Step 4B.2 — User Creation Wizard

**Frontend: `frontend/islands/UserWizard.tsx` (new)**

2-step wizard (simplified from original 4-step plan):

### Step 1: Account
- Username input (text, required)
- Password + Confirm Password (type="password", min 8 chars)
- **Collapsible "Advanced: Kubernetes Identity" section** (closed by default):
  - K8s Username (text, defaults to local username)
  - K8s Groups (comma-separated text input, defaults to "system:authenticated")
- Inline validation errors per field
- No debounced uniqueness check — submit and let backend return 409

### Step 2: Review
- Summary: username, k8s identity (if customized), roles
- "Create User" button → `POST /v1/users`
- On success: show confirmation with link to RoleBinding wizard
  - "User created. To grant permissions, create a Role Binding."
  - Button: "Create Role Binding" → navigates to `/resources/clusterrolebindings/new`
- On 409: show "Username already exists" error, allow editing
- On other error: show error message

**Route:** `frontend/routes/settings/users/new.tsx`
**Link from:** Add "Create User" button to `UserManager.tsx` header

**Files to create/modify:**
- `frontend/islands/UserWizard.tsx` (new)
- `frontend/routes/settings/users/new.tsx` (new)
- `frontend/islands/UserManager.tsx` (add "Create User" link)

---

## Step 4B.3 — RoleBinding/ClusterRoleBinding CRUD Backend

**Backend: Rename `rbac_viewer.go` → `rbac.go` (via `git mv`), add binding CRUD**

6 new handlers (NOT 12 — Roles and ClusterRoles remain read-only):

### Namespaced (RoleBinding) — 3 handlers

```go
func (h *Handler) HandleCreateRoleBinding(w, r)   // POST /resources/rolebindings/{namespace}
func (h *Handler) HandleUpdateRoleBinding(w, r)   // PUT /resources/rolebindings/{namespace}/{name}
func (h *Handler) HandleDeleteRoleBinding(w, r)   // DELETE /resources/rolebindings/{namespace}/{name}
```

### Cluster-scoped (ClusterRoleBinding) — 3 handlers

```go
func (h *Handler) HandleCreateClusterRoleBinding(w, r)   // POST /resources/clusterrolebindings
func (h *Handler) HandleUpdateClusterRoleBinding(w, r)   // PUT /resources/clusterrolebindings/{name}
func (h *Handler) HandleDeleteClusterRoleBinding(w, r)   // DELETE /resources/clusterrolebindings/{name}
```

Each handler follows existing configmaps.go pattern:
1. `requireUser(w, r)` — extract authenticated user
2. `chi.URLParam` for namespace/name
3. `h.checkAccess(w, r, user, verb, kind, ns)` — RBAC check
4. `decodeBody(w, r, &obj)` for create/update — decode into `rbacv1.RoleBinding` / `rbacv1.ClusterRoleBinding`
5. `h.impersonatingClient(user)` — impersonated clientset
6. `cs.RbacV1().RoleBindings(ns).Create/Update/Delete(ctx, ...)` — k8s API
7. `h.auditWrite(...)` — audit log
8. `writeCreated/writeData/204` — response

**Backend preview endpoint** for wizard YAML consistency:
- `POST /api/v1/wizards/rolebinding/preview` — accepts binding JSON, constructs `rbacv1.RoleBinding` or `rbacv1.ClusterRoleBinding`, returns YAML
- Add to `backend/internal/wizard/rolebinding.go` (new) following existing wizard pattern
- `RoleBindingInput` struct with `Validate()` and `ToRoleBinding()`/`ToClusterRoleBinding()` methods

**Route registration** in `routes.go`, add alongside existing RBAC GETs:
```go
ar.Post("/resources/rolebindings/{namespace}", h.HandleCreateRoleBinding)
ar.Put("/resources/rolebindings/{namespace}/{name}", h.HandleUpdateRoleBinding)
ar.Delete("/resources/rolebindings/{namespace}/{name}", h.HandleDeleteRoleBinding)
ar.Post("/resources/clusterrolebindings", h.HandleCreateClusterRoleBinding)
ar.Put("/resources/clusterrolebindings/{name}", h.HandleUpdateClusterRoleBinding)
ar.Delete("/resources/clusterrolebindings/{name}", h.HandleDeleteClusterRoleBinding)
```

**Files to modify:**
- `backend/internal/k8s/resources/rbac_viewer.go` → `rbac.go` (git mv + add CRUD)
- `backend/internal/server/routes.go` (add binding routes)
- `backend/internal/wizard/rolebinding.go` (new — preview endpoint)
- `backend/internal/wizard/handler.go` (add preview handler)

---

## Step 4B.4 — RoleBinding Wizard

**Frontend: `frontend/islands/RoleBindingWizard.tsx` (new)**

Used for both RoleBinding and ClusterRoleBinding (prop: `clusterScoped: boolean`).

### Step 1: Basics
- Name (DNS label, required)
- Namespace (dropdown, only for RoleBinding — hidden for ClusterRoleBinding)

### Step 2: Role Reference
- Searchable dropdown of existing Roles + ClusterRoles:
  - Fetch `GET /v1/resources/roles/{ns}` (for namespaced bindings) + `GET /v1/resources/clusterroles`
  - Group in dropdown: "Roles (namespace)" section + "ClusterRoles" section
  - Selected role shown as `{kind}: {name}`

### Step 3: Subjects (inline, not a shared component)
- Table of subjects, each row:
  - Kind: dropdown (User / Group / ServiceAccount)
  - Name: text input. "Add from local users" button fetches `GET /v1/users` and shows a dropdown.
  - Namespace: text input, only visible when kind=ServiceAccount
  - Remove button per row
- "Add Subject" button appends row
- **Important:** Set `subject.apiGroup` correctly: `"rbac.authorization.k8s.io"` for User/Group, `""` for ServiceAccount

### Step 4: Review
- YAML preview via `POST /v1/wizards/rolebinding/preview`
- Apply via `POST /v1/resources/rolebindings/{ns}` or `POST /v1/resources/clusterrolebindings`

**Routes:**
- `frontend/routes/resources/rolebindings/new.tsx` — renders `<RoleBindingWizard clusterScoped={false} />`
- `frontend/routes/resources/clusterrolebindings/new.tsx` — renders `<RoleBindingWizard clusterScoped={true} />`

### Resource Browser Integration
- Add `createHref` to RoleBinding and ClusterRoleBinding resource pages
- Add delete action to RBAC binding types in `action-handlers.ts`

**Files to create/modify:**
- `frontend/islands/RoleBindingWizard.tsx` (new)
- `frontend/routes/resources/rolebindings/new.tsx` (new)
- `frontend/routes/resources/clusterrolebindings/new.tsx` (new)
- `frontend/islands/UserManager.tsx` (add Create User link)
- `frontend/lib/action-handlers.ts` (add delete for binding types)
- Resource browser route pages for binding types (add `createHref`)

---

## Implementation Order

1. **4B.1** — User creation endpoint (small, standalone backend work)
2. **4B.3** — RBAC binding CRUD backend (6 handlers + preview endpoint, unblocks frontend)
3. **4B.2** — User creation wizard (2-step frontend, depends on 4B.1)
4. **4B.4** — RoleBinding wizard (4-step frontend, depends on 4B.3)

Steps 1+2 (backend) can run in parallel. Steps 3+4 (frontend) can run in parallel after backend.

---

## Files Summary

### New files (7)
- `frontend/islands/UserWizard.tsx`
- `frontend/islands/RoleBindingWizard.tsx`
- `frontend/routes/settings/users/new.tsx`
- `frontend/routes/resources/rolebindings/new.tsx`
- `frontend/routes/resources/clusterrolebindings/new.tsx`
- `backend/internal/wizard/rolebinding.go`

### Modified files (5)
- `backend/internal/server/handle_users.go` (add create handler)
- `backend/internal/k8s/resources/rbac_viewer.go` → `rbac.go` (git mv + 6 CRUD handlers)
- `backend/internal/server/routes.go` (add binding + user routes)
- `backend/internal/auth/local.go` (extend CreateUser for k8s identity)
- `backend/internal/wizard/handler.go` (add rolebinding preview)
- `frontend/islands/UserManager.tsx` (add Create User button)
- `frontend/lib/action-handlers.ts` (add delete actions)

---

## Testing Strategy

- **Backend unit tests:** table-driven tests for 6 RBAC binding handlers + user creation handler using httptest + fake clientset. Test `system:` prefix rejection, password validation, duplicate username 409.
- **Wizard preview tests:** verify RoleBindingInput validation and YAML output
- **Frontend:** `deno lint`, `deno fmt --check`, `deno check` on all new files
- **Unit tests for subject apiGroup logic:** extract the `apiGroupForKind()` function into a testable utility
- **Homelab smoke test:**
  - Create new local user via wizard, verify login works
  - Create ClusterRoleBinding assigning user to `view` role, verify impersonation returns resources
  - Delete a RoleBinding, verify removed from cluster
  - Attempt to create user with `system:` prefix, verify rejection

---

## Acceptance Criteria

- [ ] `POST /api/v1/users` creates a local user with custom k8s identity (single atomic write)
- [ ] `system:` prefixed k8s usernames rejected, `system:masters` group rejected
- [ ] User wizard creates user in 2 steps (Account + Review)
- [ ] Success page links to RoleBinding wizard for role assignment
- [ ] RoleBinding/ClusterRoleBinding CRUD works (6 endpoints)
- [ ] RoleBinding wizard builds valid bindings with role reference + subjects
- [ ] Subject apiGroup set correctly (rbac.authorization.k8s.io for User/Group, "" for SA)
- [ ] YAML preview via backend endpoint (consistent with wizard pipeline)
- [ ] Delete actions available on binding resource browser pages
- [ ] All Go tests pass, all frontend lint/type checks pass
- [ ] Homelab smoke test passes
