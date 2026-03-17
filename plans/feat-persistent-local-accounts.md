# Persist Local User Accounts in PostgreSQL

## Problem

Local user accounts are stored in-memory by `LocalProvider`. Every backend pod restart loses all accounts, requiring manual re-creation via `/setup/init`.

## Proposed Solution (revised per reviewer feedback)

Add a `local_users` table and modify `LocalProvider` to query PostgreSQL directly. **No in-memory cache** — a primary-key lookup on a table with a handful of rows is sub-millisecond, invisible behind Argon2id's 100ms+ hash time. **PostgreSQL is required** — it already is for audit logs, settings, and cluster registry.

## Implementation

### 1. SQL Migration (`backend/internal/store/migrations/000004_create_local_users.up.sql`)

```sql
CREATE TABLE local_users (
    id              TEXT PRIMARY KEY,
    username        TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    salt            TEXT NOT NULL,
    k8s_username    TEXT NOT NULL,
    k8s_groups      TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    roles           TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Down migration: `DROP TABLE IF EXISTS local_users;`

### 2. User Store (`backend/internal/store/users.go`)

4 methods only (no List, Delete — add when user management UI exists):
- `Create(ctx, user) error` — INSERT, return `ErrDuplicateUser` on unique violation
- `GetByUsername(ctx, username) (*UserRecord, error)`
- `GetByID(ctx, id) (*UserRecord, error)`
- `Count(ctx) (int, error)`

`CreateFirstUser` atomicity via DB: `INSERT INTO local_users ... WHERE (SELECT COUNT(*) FROM local_users) = 0` — inherently atomic, no Go mutex needed.

### 3. Modify `LocalProvider` (`backend/internal/auth/local.go`)

- **Remove** `sync.RWMutex`, `users map`, `usersByID map`
- **Remove** `LoadFromDB` — not needed, queries go direct to DB
- **Add** required `UserStore` field (not optional — PostgreSQL is a hard dependency)
- `Authenticate`: calls `store.GetByUsername`, then Argon2id comparison
- `CreateFirstUser`: calls `store.CreateFirstUser` (DB-level atomicity)
- `CreateUser`: calls `store.Create`
- `GetUserByID`: calls `store.GetByID`
- `UserCount`: calls `store.Count`

### 4. Store Type (`backend/internal/store/users.go`)

```go
type UserRecord struct {
    ID             string
    Username       string
    PasswordHash   string
    Salt           string
    K8sUsername    string
    K8sGroups     []string
    Roles         []string
    CreatedAt     time.Time
}
```

Conversion between `UserRecord` and `auth.User` happens at the `LocalProvider` boundary (matches `ClusterStore` pattern).

### 5. Startup Wiring (`backend/cmd/kubecenter/main.go`)

- Create `UserStore` from existing DB pool (unconditional — no nil checks)
- Pass to `NewLocalProvider(userStore, logger)`

## Acceptance Criteria

- [ ] Admin account survives backend pod restart
- [ ] `POST /setup/init` creates user in PostgreSQL (DB-level atomicity)
- [ ] Login works immediately after pod restart (no re-setup needed)
- [ ] Existing LocalProvider tests updated to use mock UserStore
- [ ] Concurrent `CreateFirstUser` calls: exactly one succeeds

## Files Changed

| File | Change |
|------|--------|
| `store/migrations/000004_create_local_users.up.sql` | New migration |
| `store/migrations/000004_create_local_users.down.sql` | Drop table |
| `store/users.go` | New UserStore (4 methods) |
| `auth/local.go` | Remove maps/mutex, query DB directly |
| `cmd/kubecenter/main.go` | Wire UserStore into LocalProvider |

## Reviewer Feedback Applied

- **DHH**: Dropped in-memory cache, dropped optional store fallback, DB-level atomicity
- **Kieran**: NOT NULL on created_at, k8s_groups default from Go not schema, UserRecord type in store package
- **Simplicity**: Cut to 4 methods, dropped updated_at, dropped List/Delete
