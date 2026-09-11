---
title: "Feature Expansion — Consolidated Execution Order"
parent_plan: docs/plans/2026-09-10-1315-feat-feature-expansion-plan.md
date: 2026-09-10
baseline_revision: b9eb8171
status: active
---

# Feature Expansion — Consolidated Execution Order

Six release-level implementation plans were produced from the master plan. This
document is the single source of truth for **what order they execute in**, the
**global decisions** that all six share, and the **cross-release conflicts** that
make certain units non-parallelizable.

Per-unit detail lives in the release plans; this file never duplicates it.

| Release | Plan | PRs |
|---|---|---|
| A — Personal saved views and pins | `2026-09-10-release-a-saved-workspaces-impl.md` | 7 |
| B — ESO evidence completion | `2026-09-10-release-b-eso-evidence-impl.md` | 9 |
| C — Remote-cluster workflow parity | `2026-09-10-release-c-remote-workflow-impl.md` | 9 |
| D — Persistent incident investigations | `2026-09-10-release-d-incidents-impl.md` | 12 |
| E — GitOps-aware tracked changes | `2026-09-10-release-e-tracked-changes-impl.md` | 8 |
| F — Backup assurance and readiness | `2026-09-10-release-f-backup-assurance-impl.md` | 10 |

**Total: 55 PRs + U0 = 56.**

---

## Baseline

Verified green on `main` @ `b9eb8171` before any work started:

- `cd backend && go vet ./...` — clean
- `cd backend && go test ./...` — no FAIL lines
- `cd frontend && deno task check` — exit 0

Any failure after this point is attributable to the change that introduced it.

---

## Global Decisions

These were settled once, at the orchestration level, because multiple release
plans proposed mutually incompatible versions.

### G1. Q1 access/retention policy — RESOLVED

The master plan's proposed default is accepted: personal ownership plus explicit
collaborator grants; **every read re-checks the caller's current Kubernetes
authorization at access time** (a grant alone never suffices); 30-day
configurable retention; stricter filtering for Secret-derived evidence.

Release D states this normatively as P1–P16. Release E applies the same policy to
change receipts. Neither may drift from it.

Consequence: Releases D and E are unblocked.

### G2. One PR per unit

Each unit is one feature branch, one PR, ≤5 touched files including tests. CI
green before the next unit starts. Branch names are specified per unit in the
release plans.

### G3. Database test harness — env-gated, NOT build-tagged

Releases A, B, D, E and F all add tables, and the Verification Contract requires
real PostgreSQL migration round trips. Three planners proposed three different
harnesses. The decision:

- Gate on **`KUBECENTER_TEST_DATABASE_URL`**. Absent → `t.Skip`.
- **No `//go:build` tag.**

Rationale: the repo's canonical check is repo-wide `go test ./...` (Agent
Directive 4). A build tag means these tests never run under the canonical
command, which defeats the purpose. Env-gating plus a CI service gets them
running in CI while local developers without a database skip cleanly.

Pure/hermetic unit tests are always-on and unaffected by the gate.

### G4. Migration sequences — pre-assigned, do not improvise

| Sequence | Release | Migration |
|---|---|---|
| `000018` | A | `create_user_preferences` |
| `000019` | B | `scope_eso_history` (index-only; no column change, no backfill) |
| `000020` | D | `create_incidents` |
| `000021` | E | `create_change_receipts` |
| `000022` | F | `create_backup_assurance` |
| — | C | none |

Last existing migration is `000017`. Any unit needing an unlisted sequence stops
and escalates rather than picking a number.

`migrations/NOTES.txt` must be updated for every operator-visible migration —
repo convention, and omitted from four of the master plan's file lists.

### G5. "DB unavailable" must be 503, never 404

`routes.go` gates optional handlers with `if s.XHandler != nil { register }`. A
no-database deployment therefore returns chi's bare 404, indistinguishable from
"no such record" — a direct R3 violation.

All new persistence-backed handlers follow the real in-repo precedent
(`externalsecrets/bulk.go:294`): **always register, always construct, and gate
each method** on a `requireStore` helper returning `503` with an explicit
`reason` code.

### G6. Mobile compatibility is a hard constraint (R4)

Two concrete traps found during planning:

- `mobile/.../dashboard_repository.dart` detects local-only by matching HTTP 400
  **plus the literal string `"local cluster"`**. Release C's remote dashboard
  must stay behind an opt-in `?coverage=1` rather than changing that response.
- `diagnostics.Result` is decoded by 12 mobile files, 4 mobile tests and 5 web
  files. U20 is **strictly additive**, proven by a `Denormalize(Normalize(x)) == x`
  round-trip test, with `rules_test.go` and `rbac_p3_test.go` passing unmodified.

### G7. Authorization helper

Read-time re-authorization uses **`CanAccessGroupResource`**, never `CanAccess`
— `CanAccess` short-circuits to allow in predicate-fake mode (`access.go:111`),
which would make partial-RBAC tests vacuous.

### G8. Fuzzing

Per `docs/solutions/backend-resilience-conventions.md`, new parse seams over
attacker-influenceable input get an in-package `*_fuzz_test.go` **and** a
`fuzz.yml` matrix row. Without the matrix row the `-list` drift guard never runs
the target and the fuzzing is decorative. The row is part of the unit, not a
follow-up.

---

## Execution Order

User-selected release order is **A → B → C → F → E → D**, with one orchestration
correction.

### Correction: U20 is pulled forward

Release E's verification consumes **U20** (normalized diagnostic check results),
which is the first unit of Release D. Running E before D would leave E without
its dependency, and E's own plan freezes the check-result tags once U27 ships.

U20 is standalone (`Depends on: None` in the master plan) and strictly additive,
so it executes **before Release E** rather than reordering whole releases.

### Sequence

```
U0                      prep — DB test harness + CI Postgres service
Release A               U1 → U2 → U3 → U4 → U5a → U5b
Release B               U13 → U14a → U14b → U15 → U16 → U17 → U18 → U19a → U19b
Release C               U7 → U8 → U9a → U9b → U10 → U11a → U11b → U11c → U12*
Release F               U32 → U32b → U33 → U34a → U34b → U34c → U35 → U36 → U36b → U37**
U20                     pulled forward from Release D
Release E               U26 → U27 → U28 → U29a → U29b → U30a → U30b → U31
Release D (remainder)   U21a → U21b → U22a → U22b → U23a → U23b → U24a → U24b → U24c → U25a → U25b
```

\* U12's live two-cluster run is gated on Q2 (unanswered). The fixture, script
and docs land; the live run is a documented human runbook.

\*\* U37 is documentation + fixture only. It authorizes **no live restore**.
Rehearsal execution stays gated on Q3 (unanswered).

### U0 — prep unit (2 files)

- `backend/internal/store/testdb_test.go` — env-gated harness per G3, unique
  per-test owner prefixes for isolation.
- `.github/workflows/ci.yml` — add a `postgres:17-alpine` service to the backend
  job and set `KUBECENTER_TEST_DATABASE_URL`.

`ci.yml` currently has **no `services:` block** and runs
`go test ./... -race -cover -count=1` against no database. `e2e.yml` already runs
`postgres:17-alpine` and is the precedent to copy.

Every later persistence unit inherits this harness. Without U0, the migration
round-trip tests in A, B, D, E and F silently skip.

---

## Cross-Release Conflicts

Units touching these files must not run concurrently across releases. Serialize
on the execution order above.

| File | Claimed by |
|---|---|
| `backend/internal/server/routes.go` | A/U2, B/U14a, B/U15, C/U8, D/U23a, E/U29a, F/U35 |
| `backend/internal/server/server.go` | A/U2, C/U8, D/U23a, E/U30b, F/U35 (**not** F/U35 per its own correction — already wired) |
| `backend/cmd/kubecenter/main.go` | A/U3, B/U14a, D/U23a, D/U25b, E/U29a, F/U34c |
| `backend/internal/yaml/handler.go` | C/U9a, C/U9b, E/U30a — **C must land before E** |
| `frontend/islands/DashboardV2.tsx` | A/U6 (deferred), C/U11b |
| `frontend/islands/YamlApplyPage.tsx` | C/U11b, E/U31 |
| `frontend/lib/api.ts` | C/U11a, D/U24a |
| `backend/internal/notifications/service.go` | F/U34b — shared by all notification sources; regression coverage required |

---

## Pre-Existing Defect Docket

Found during planning. **Not chartered by this roadmap, not folded into any
feature PR.** Listed so they are not lost, and so nobody mistakes them for
regressions introduced by this work.

| # | Defect | Evidence | Impact |
|---|---|---|---|
| 1 | Events tab sends `involvedObjectKind`/`involvedObjectName`; backend never reads them | `ResourceDetail.tsx:429-431` vs `resources/handler.go:115`; string absent from all backend Go | Every resource detail page shows all namespace events, not the resource's |
| 2 | `DedupExists` keys omit cluster ID and UID | `notifications/store.go:49-65` | Multi-cluster: a same-named resource failing in a second cluster is silently suppressed. Affects every notification source |
| 3 | No web cluster switcher — `selectedCluster` is never assigned | read at `TopBarV2.tsx:98`, `api.ts:142`, `resource-counts.ts:85`; written only at `cluster.ts:13` | The entire shipped multi-cluster backend is unreachable from the UI |
| 4 | 3 E2E specs outside Playwright's `testDir` | `playwright.config.ts:4` = `./tests`; `velero.spec.ts`, `flux-notifications.spec.ts`, `namespace-limits.spec.ts` at `e2e/` root | 893 lines of tests never execute |
| 5 | `Schedule.LastBackupPhase` never written; `IsSuccessPhase` accepts BSL/Schedule phases | `velero/types.go:97`, `types.go:144-150` | Dead field feeding UI; helper cannot classify backup outcomes |

\#3 is the highest-value fix independent of this roadmap: it gates whether
Release C is reachable by any user. Release C's U11a builds the switcher as part
of the track.

Also corrected: `CLAUDE.md:258` claims "AccessChecker queries local cluster RBAC,
not remote". This is **stale** — `access.go:258-265` routes remote SARs, wired at
`main.go:437`. Update when Release C ships.

---

## Open Gates

| ID | Blocks | State |
|---|---|---|
| Q1 | D, E receipts | **Resolved** — see G1 |
| Q2 | C/U12 live validation only | Open — source work proceeds against fixtures |
| Q3 | All rehearsal execution | Open — U37 stays docs-only |
| Q4 | Git patch/PR extension | Open — U26 delivers read-only ownership |
| Q5 | A/U38 + A/U6 dashboard layouts | Open — deferred appendix |
