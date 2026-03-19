# feat: User Management UI

Admin-only user list, delete, and password change. Backend handlers + frontend island + sidebar nav.

## Problem Statement

The user store (PostgreSQL) already has `List`, `Delete`, and `UpdatePassword` methods (PR #44), but there's no way to manage users except through the initial setup endpoint. Admins need a UI to view all local users, delete stale accounts, and reset passwords.

## Proposed Solution

Add a `/settings/users` page with a user table, delete (type-name-to-confirm), and password change modal. Three admin-only API endpoints back the UI.

---

## Implementation Plan

### Step 1: Backend — LocalProvider + Handlers + Routes

**`backend/internal/auth/local.go`:**
- Add `Store() UserStore` getter to expose the store for List/Delete
- Add `UpdatePassword(ctx, id, newPassword) error` method — validates password (8-128 chars), hashes with Argon2id (reuses `hashSem`), calls store. This is the single place for password validation.
- Extract `hashPassword(ctx, password) (string, error)` from `hashAndBuild` if not already separate

**Create `backend/internal/server/handle_users.go`** — three admin-only handlers:

| Endpoint | Handler | Notes |
|----------|---------|-------|
| `GET /api/v1/users` | `handleListUsers` | Calls `s.LocalAuth.Store().List(ctx)`. Returns `[]UserRecord` (PasswordPHC is `json:"-"`). |
| `DELETE /api/v1/users/{id}` | `handleDeleteUser` | Guards: no self-delete, no last-admin-delete. Last-admin check uses `SELECT ... FOR UPDATE` in a single transaction to prevent race. Audit logs. Returns 204. |
| `PUT /api/v1/users/{id}/password` | `handleUpdateUserPassword` | Decodes `{"password": "..."}`, calls `s.LocalAuth.UpdatePassword()`. Maps provider errors to HTTP (validation → 400, not found → 404). Audit logs. |

**`backend/internal/server/routes.go`** — register within auth+CSRF group:

```go
ar.Route("/users", func(ur chi.Router) {
    ur.Use(middleware.RequireAdmin)
    ur.Get("/", s.handleListUsers)
    ur.Delete("/{id}", s.handleDeleteUser)
    ur.Put("/{id}/password", s.handleUpdateUserPassword)
})
```

**Last-admin guard implementation:** The delete handler must atomically check admin count and delete. Use a single PostgreSQL transaction:
1. `SELECT COUNT(*) FROM local_users WHERE 'admin' = ANY(roles) FOR UPDATE`
2. If count <= 1, rollback and return 409 Conflict
3. Otherwise, `DELETE FROM local_users WHERE id = $1`
4. Commit

This requires either a new `DeleteWithAdminGuard(ctx, id) error` method on the store, or the handler acquires a transaction directly. Prefer adding it to the store since the guard is a data integrity concern.

### Step 2: Backend — Tests

**Create `backend/internal/server/handle_users_test.go`**

Test with `httptest` + `MemoryUserStore` (same pattern as `handle_auth_test.go`):

1. List users — create 2 users, GET /users, verify both returned, no password in JSON
2. Delete user — create user, DELETE /users/{id}, verify 204, verify gone
3. Delete self — verify rejection (409)
4. Delete last admin — verify rejection (409)
5. Update password — PUT /users/{id}/password, verify 200, verify can login with new password
6. Update password — too short, verify 400
7. Non-admin access — verify 403 for all three endpoints

### Step 3: Frontend — Fix `api.ts` 204 handling + Extract ConfirmDialog

**Fix `frontend/lib/api.ts`** — line 132 calls `res.json()` unconditionally, which throws `SyntaxError` on 204 No Content responses. Fix:

```typescript
if (res.status === 204) {
    return {} as APIResponse<T>;
}
return await res.json();
```

This is a **latent bug** affecting all existing delete operations (they work by accident because callers catch and ignore the error).

**Create `frontend/components/ui/ConfirmDialog.tsx`** — extract from ResourceTable.tsx (lines 530-596):

```typescript
interface ConfirmDialogProps {
    title: string;
    message?: string;
    confirmLabel: string;
    danger?: boolean;
    typeToConfirm?: string;  // if set, user must type this to enable confirm
    loading?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
}
```

Refactor ResourceTable.tsx to use it. Include keyboard accessibility: focus trap, Escape to close, auto-focus first input.

### Step 4: Frontend — UserManager Island + Route + Nav

**Create `frontend/lib/user-types.ts`:**

```typescript
export interface LocalUser {
    id: string;
    username: string;
    k8sUsername: string;
    k8sGroups: string[];
    roles: string[];
}
```

**Create `frontend/islands/UserManager.tsx`:**

State — use discriminated union for dialog, 6 signals total:
- `users`, `loading`, `error` — data signals
- `dialog: DialogState` — `{ kind: "idle" } | { kind: "confirmDelete", user, input } | { kind: "changePassword", user, password }`
- `actionLoading` — boolean
- `toast: { message, type, ts } | null`

Layout:
- Table: Username, Kubernetes Identity, Groups, Roles, Actions
- **Inline buttons** per row (not kebab — only 2 fixed actions): "Change Password" (ghost), "Delete" (danger ghost)
- Current user's row shows "(you)" badge, delete button disabled
- Delete uses extracted `ConfirmDialog` with type-name-to-confirm
- Password change uses a simple modal with one password input
- Toast for success/failure
- Optimistic row removal on delete (restore on error)

**Create `frontend/routes/settings/users.tsx`** — follow `auth.tsx` pattern.

**`frontend/lib/constants.ts`** — add "Users" to Settings section in sidebar nav.

---

## Acceptance Criteria

- [ ] `GET /api/v1/users` returns all local users (no password data) — admin only
- [ ] `DELETE /api/v1/users/{id}` deletes user — prevents self-delete and last-admin (atomically)
- [ ] `PUT /api/v1/users/{id}/password` changes password — validates in LocalProvider only
- [ ] All three endpoints return 403 for non-admin users
- [ ] All write operations are audit logged
- [ ] Frontend user table at `/settings/users` with inline action buttons
- [ ] Delete uses extracted `ConfirmDialog` component (also used by ResourceTable)
- [ ] Password change modal with 8-char minimum validation
- [ ] Current user row shows "(you)" badge, delete disabled
- [ ] `api.ts` correctly handles 204 No Content (fixes latent bug)
- [ ] Sidebar has "Users" link in Settings section
- [ ] `go test ./... -race` passes
- [ ] `deno lint && deno fmt --check && deno task build` passes

## Files to Create/Modify

| File | Action | Purpose |
|------|--------|---------|
| `backend/internal/auth/local.go` | Modify | Add `Store()` getter + `UpdatePassword` + extract `hashPassword` |
| `backend/internal/server/handle_users.go` | Create | Three admin-only handlers |
| `backend/internal/server/handle_users_test.go` | Create | 7 httptest integration tests |
| `backend/internal/server/routes.go` | Modify | Register /users routes |
| `frontend/lib/api.ts` | Modify | Fix 204 No Content handling |
| `frontend/lib/user-types.ts` | Create | LocalUser interface |
| `frontend/components/ui/ConfirmDialog.tsx` | Create | Extracted reusable confirm dialog |
| `frontend/islands/ResourceTable.tsx` | Modify | Use extracted ConfirmDialog |
| `frontend/islands/UserManager.tsx` | Create | User management island |
| `frontend/routes/settings/users.tsx` | Create | Route page |
| `frontend/lib/constants.ts` | Modify | Add sidebar nav entry |

## Notes

- Only manages local users. OIDC/LDAP users are managed externally.
- Deleted user tokens expire naturally (15 min access). No session store changes needed.
- User creation UI is a separate future concern (currently only via `/setup/init`).
