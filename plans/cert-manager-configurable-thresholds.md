# feat: Configurable Cert-Manager Expiry Thresholds

## Overview

Replace the hardcoded 30-day warning / 7-day critical expiry thresholds in `backend/internal/certmanager/poller.go` with user-configurable values stored in `app_settings`. Admin-only write access via `PUT /settings/cert-manager`; all authenticated users can read.

Sibling of `plans/cert-manager-wizards-phase-11b.md`. Split out because it shares no code path with the wizard pipeline.

Roadmap item **#7b** (thresholds half).

## Problem Statement

`backend/internal/certmanager/types.go` defines:

```go
WarningThresholdDays  = 30  // line 50
CriticalThresholdDays = 7   // line 53
```

These are consumed by `poller.go:41-52` (`thresholdBucket()`) and drive notification emission. Different teams want different lead times:

- Compliance-driven shops want wider windows (60/14 or 90/30) for change-management sign-off.
- Aggressive rotators on short-lived ACME certs want tighter windows (14/3) to avoid alert fatigue.

Hardcoded values require recompiling the backend.

### The dedupe-vs-threshold-change bug (Kieran catch)

The poller currently dedupes notifications by `(uid, thresholdBucket)` where `thresholdBucket` derives from the hardcoded day count. If an operator widens `warning` from 30 → 60:

- Certificate at 45 days to expiry was **not** yet notified (30-day bucket not reached).
- Under new 60-day threshold, it *should* fire immediately.
- Under current dedupe logic with a naive refactor, the change of threshold constants doesn't create a new bucket key — the cert silently goes un-notified until 30 days.

The fix: dedupe key must include the **threshold value itself**, not a derived bucket. Changing threshold creates a fresh dedupe-key space and fires once per eligible cert.

## Proposed Solution

Three moving parts:

1. **Schema** — add `cert_manager_warning_days` and `cert_manager_critical_days` columns to `app_settings`.
2. **API** — `GET/PUT /settings/cert-manager` with admin gate and validation.
3. **Poller refactor** — read live values per tick, include threshold days in dedupe key.

No frontend UI in v1. Operators edit via curl; release notes will include a one-liner. A settings UI section can be added as a follow-up if users actually adjust these often.

## Technical Approach

### Schema migration

`backend/internal/store/migrations/0000NN_cert_manager_thresholds.up.sql`:

```sql
ALTER TABLE app_settings
  ADD COLUMN cert_manager_warning_days  INT NOT NULL DEFAULT 30,
  ADD COLUMN cert_manager_critical_days INT NOT NULL DEFAULT  7;
```

Plus `.down.sql` dropping both columns.

### Store

Extend `AppSettings` struct in `backend/internal/store/settings.go`:

```go
CertManagerWarningDays  int `json:"certManagerWarningDays"`
CertManagerCriticalDays int `json:"certManagerCriticalDays"`
```

Existing `Get()` / `Update()` round-trip unchanged; cache invalidation already handled.

### HTTP handler

New `backend/internal/server/handle_certmanager_settings.go`:

- `GET /settings/cert-manager` — any authenticated user, returns `{warningDays, criticalDays}`.
- `PUT /settings/cert-manager` — admin role required, CSRF-protected, JSON body `{warningDays, criticalDays}`, writes audit log entry with action `settings.cert-manager.update`.

Validation:

| Rule | Message |
|---|---|
| `warningDays ∈ [2, 365]` | "warning must be 2-365 days" |
| `criticalDays ∈ [1, 364]` | "critical must be 1-364 days" |
| `criticalDays < warningDays` | "critical must be less than warning" |
| JSON decode errors | 400 with field path |

### Poller refactor

`backend/internal/certmanager/poller.go`:

**Delete** constants `WarningThresholdDays` / `CriticalThresholdDays` in `types.go:50,53`.

**Refactor** `thresholdBucket(daysUntilExpiry int)` → `thresholdBucket(daysUntilExpiry, warningDays, criticalDays int) (level, thresholdDays int, ok bool)`. Returns level (`"warning"` / `"critical"`) and the threshold days it crossed (for the dedupe key).

**Refactor** `run()` to fetch current settings from `SettingsStore` at the start of each 60s tick:

```go
settings, err := p.settings.Get(ctx)
if err != nil {
    slog.Error("cert-manager poller: failed to read settings, using defaults", "err", err)
    settings = store.AppSettings{CertManagerWarningDays: 30, CertManagerCriticalDays: 7}
}
```

**Change dedupe key** in emitter (`poller.go:138-178`) from `(uid, bucket)` to `(uid, thresholdDays)` — concretely the map/set key becomes `fmt.Sprintf("%s:%d", cert.UID, thresholdDays)`. This naturally:

- Fires once per cert per distinct threshold value.
- Refires when an operator changes warning 30 → 60 (new key `uid:60` isn't in the dedupe set).
- Does NOT refire if operator flips 30 → 60 → 30 (key `uid:30` already present from the original notification).

Document in release notes: "Widening thresholds will cause certificates already in the new window to fire one additional notification per certificate."

### Dependency injection

Poller constructor currently takes k8s client + notification service. Add a `SettingsStore` interface with a single `Get(ctx) (AppSettings, error)` method. Wire in `cmd/kubecenter/main.go` where poller is constructed. Interface-based so tests can inject a fake.

## Acceptance Criteria

### API

- [ ] `GET /settings/cert-manager` returns `{warningDays: 30, criticalDays: 7}` on fresh install.
- [ ] `PUT /settings/cert-manager` with admin + valid body (e.g. `{warningDays: 60, criticalDays: 14}`) returns 200, persists, and `GET` reflects the change.
- [ ] `PUT` from non-admin returns 403.
- [ ] `PUT` without CSRF header returns 403.
- [ ] Validation rejects: `warningDays=0`, `warningDays=400`, `criticalDays >= warningDays`, non-int body fields, missing fields — each returns 400 with field-level message.
- [ ] Audit log records admin mutations with action `settings.cert-manager.update`, before/after values (masked where appropriate).

### Store / migration

- [ ] Migration `.up.sql` and `.down.sql` round-trip clean on empty DB and on DB with existing rows.
- [ ] Existing installs get the default 30/7 via column `DEFAULT`.
- [ ] `AppSettings` round-trips through JSON marshal/unmarshal with new fields.

### Poller behavior

- [ ] `thresholdBucket` unit tests cover boundaries: 0d, 1d, `criticalDays`, `criticalDays+1`, `warningDays`, `warningDays+1`, 365d.
- [ ] `thresholdBucket` table test for 3 threshold pairs: (30,7), (60,14), (14,3).
- [ ] Integration test: configure poller with fake `SettingsStore`, fake notification sink, synthetic certs at various days-to-expiry. Change settings mid-run; assert next tick uses new values without restart.
- [ ] Integration test: dedupe — same cert at same threshold fires once; widening threshold fires once for eligible certs; narrowing does not backfire for already-notified cert.
- [ ] Poller tolerates `SettingsStore.Get` errors by falling back to 30/7 defaults and logging at ERROR; does not crash.

### Non-functional

- [ ] `go vet`, `go test ./...`, `deno lint`, `deno fmt --check` all pass.
- [ ] No frontend changes in this PR (zero Deno diff).
- [ ] Release notes include the dedupe-on-widen behavior and a curl example.

## Dependencies & Risks

- **Dependency:** Phase 11A cert-manager package. This plan modifies `poller.go` and `types.go` in that package.
- **Risk (low):** Dedupe state is in-memory only (no persistence across restarts). Restart between a threshold change and next tick produces no extra notifications — acceptable, matches current behavior.
- **Risk (low):** Settings read on every 60s tick adds one PostgreSQL query per tick. Negligible; `settings` table has 1 row.

## Out of Scope (Deferred)

- **Settings UI** — curl is sufficient for v1. Add UI section if users adjust these often.
- **Per-certificate threshold overrides** (e.g., annotation-driven) — YAGNI until someone asks.
- **Threshold history / audit trail beyond the standard audit log.**

## References

### Internal

- Hardcoded thresholds to remove: `backend/internal/certmanager/types.go:50,53`
- Poller threshold logic: `backend/internal/certmanager/poller.go:41-52`
- Poller emitter: `backend/internal/certmanager/poller.go:138-178`
- Settings storage: `backend/internal/store/settings.go`
- Migration precedent: `backend/internal/store/migrations/000002_create_settings.up.sql`
- Settings handler pattern: `backend/internal/server/handle_settings.go` (auth settings handler)
- Audit logger: `backend/internal/audit/`

### Related

- Sibling plan: `plans/cert-manager-wizards-phase-11b.md`
- Phase 11A: Cert-Manager Observatory
