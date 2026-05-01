---
name: Phase E P3 cleanups — small fixes, naming, a11y, missing test scenarios
status: completed
priority: p3
issue_id: 355
tags: [code-review, eso, phase-e, cleanup, follow-up]
dependencies: []
---

## Resolution Summary (2026-05-01)

Landed in this sweep:
- **Item 1** (double WriteHeader): switched force-sync 202 to `httputil.WriteJSON(w, 202, ...)`.
- **Item 2** (future-skewed refreshTime): clamped to non-negative in `patchForceSyncOnce`.
- **Item 3** (RBAC verb): pre-check verb now `patch` everywhere.
- **Item 4** (succeeded_count): renamed to `succeededCount` (camelCase).
- **Item 5** (ScopeTgt → ScopeTarget): repo-wide rename in BulkJobMessage.
- **Item 6** (strings.Cut): both byte-walk sites replaced.
- **Item 7** (InvalidateCache storms): single force-sync no longer invalidates per call (30s TTL).
- **Item 8** (silent error swallowing): subsumed by #345.
- **Item 9** (in-flight submit guard): disabled state + early-return guard added.
- **Item 10** (Esc handler): `keydown` listener calls onClose on Escape.
- **Item 11** (errorField helper): added `errorExtra(err, key)` in `lib/api.ts`.
- **Item 13** (empty-body bypass): subsumed by #340.
- **Item 15** (apierrorReason deletion): subsumed by #338.

Deferred (not in scope for this sweep):
- **Item 12** (`as never` discriminated union): wire stays the same; would require an overload-based redesign of `bulkRefresh` API client.
- **Item 14** (BulkWorkerEnqueuer interface): kept as-is per "preserve if you anticipate growth."
- **Items 16–21** (test scenarios): partially landed via #347/#348/#349/#352/#354. The remaining scenarios (RBAC partial-namespace `Restricted`, 5000-cap, ClusterStore/Namespace handler tests, dialog state machine, 1000-target audit render, audit-detail Reason assertion) need a dedicated test-coverage PR.
- **Items 22–25** (CLAUDE.md/README updates): track separately; high-volume Phase E doc refresh deserves its own commit so reviewers can scan it.

## Problem Statement

Grouped follow-up for the discretionary P3 findings from the Phase E ce-review (run 20260501-134644-eso-phase-e). Each item is small and locally fixable; bundling them avoids 21 separate todo files for items that are individually trivial.

Pick the items that fit into a single "Phase E cleanup" PR; defer the rest.

## Items

### Code fixes (safe_auto candidates)

1. **Force-sync handler double WriteHeader** (`actions.go:131`) — `WriteHeader(202)` + `WriteData` (which calls `WriteHeader(200)`). Logs "superfluous-WriteHeader" warning. Fix: replace `WriteData` call with inline `json.NewEncoder(w).Encode(...)` after `WriteHeader(202)`. Or add `httputil.WriteJSONStatus(w, status, data)` helper. *(correctness P3 + api-contract P3)*

2. **Future-skewed refreshTime causes permanent 409** (`actions.go:227-235`) — `time.Since(parsed)` is negative when parsed is in the future; `< 30s` is true → permanent in-flight. NTP step or malicious controller writes future timestamp → all bulk-refresh targets report `skipped:already_refreshing` indefinitely. Fix: clamp `time.Since(parsed) >= 0 && < inFlightWindow`. *(adversarial conf 0.90)*

3. **RBAC pre-check verb mismatch** (`actions.go:169` + `bulk.go:165`) — pre-check uses `update`, K8s call uses `Patch`. Distinct K8s RBAC verbs. User with `patch` but not `update` gets false denial. Fix: change pre-check verb to `patch`. *(security low)*

4. **succeeded_count snake_case in audit Detail** (`bulk.go:514`) — mixed with camelCase siblings. Fix: rename to `succeededCount`; update `TestBulkRefresh_AuditDetailShape`. *(api-contract + maintainability, both 2x)*

5. **ScopeTgt vs ScopeTarget naming** (`bulk_worker.go:52` BulkJobMessage) — abbreviation forces context switch in every reader. Wire/store use `ScopeTarget`. Fix: rename to `ScopeTarget`. *(maintainability conf 0.85)*

6. **Hand-rolled byte split** (`bulk.go:215`, `bulk.go:1153`) — manually walks scopeTarget for `/`. Replace with `strings.Cut(scopeTarget, "/")`. Two sites. *(maintainability conf 0.80)*

7. **InvalidateCache storms on single force-sync** (`actions.go:174`) — every successful force-sync calls `h.InvalidateCache()`. A force-sync storm of 50 ESes triggers 50 SA fetchAlls. Bulk worker invalidates ONCE at end. Fix: drop the InvalidateCache call entirely (30s TTL is acceptable) OR coalesce with a debounced future invalidation. *(performance conf 0.80)*

8. **Discarded `_ =` AppendOutcome / Complete errors** (`bulk_worker.go` 11 sites) — silent failure on pool exhaustion. Fix: log at Warn level instead of swallowing. Already covered by #345 if implemented as Option A+C; track here if that route doesn't include it.

9. **Submit button no in-flight guard** (`ESOBulkRefreshDialog.tsx:111`) — fast double-click can fire two POSTs. Server's 409 active_job_exists is the safety net the UI shouldn't be racing. Fix: `disabled={phase.value !== 'confirm'}` on the Refresh button + early-return guard in `submit`. *(kieran-typescript conf 0.85)*

10. **Modal lacks focus trap and Esc handler** (`ESOBulkRefreshDialog.tsx`) — a destructive bulk-write affordance with no keyboard escape. Fix: add `keydown` listener on `globalThis.document` calling `onClose()` on `Escape`. Defer focus trap. *(kieran-typescript conf 0.72)*

11. **`ApiError.body` typed `Record<string, unknown>`** — every consumer narrows with unsafe `as string` casts (2+ sites in this PR alone). Fix: add `errorField(err: ApiError, key: string): string | undefined` helper in `lib/api.ts`. *(maintainability + kieran-typescript 2x)*

12. **bulkRefresh `target as never` discriminated union** (`eso-api.ts:135-159`, `ESOBulkRefreshDialog.tsx:117`) — non-discriminated union forces `as never` cast at the call site. Fix: discriminate on action with overloads or a union type. Subsumed by #340 if accepted (touches the same path). *(kieran-typescript conf 0.88)*

13. **bulkRefresh body decode skipped when ContentLength == 0** (`bulk.go:319`) — chunked-transfer or Content-Length-less POSTs silently bypass the pin check. Subsumed by #340 if Option A is accepted (require non-empty TargetUIDs). *(correctness conf 0.70)*

14. **`BulkWorkerEnqueuer` interface marginal value** (`bulk_worker.go:44`) — single-method interface, single production impl + one test fake. Could be replaced with an exported `Enqueue func` field on Handler. Optional cleanup; preserve if you anticipate growth. *(maintainability conf 0.70)*

15. **`apierrorReason` deletion** — once #338 lands, the empty stub disappears entirely.

### Test scenarios (T-* findings)

16. **Plan AE5 — RBAC partial-namespace `Restricted` flag** — tests use AlwaysAllow or AlwaysDeny only. Add a test where AccessChecker permits ns `apps` but denies `platform`; assert `VisibleCount < TotalCount`, `Restricted == true`. *(testing T-5)*

17. **maxBulkTargets = 5000 cap (413 response)** — no test seeds >5000 ESes or stubs scope. Add minimal test using a stub that returns >5000. *(testing T-6)*

18. **`HandleBulkRefreshClusterStore` and `HandleBulkRefreshNamespace`** — only the per-store path has handler tests. Add namespace + cluster-store happy-path tests; assert `audit.Action` and `bulkAuditNamespace` per scope. *(testing T-4)*

19. **Frontend ESOBulkRefreshDialog state machine tests** — 5-phase flow + two 409 recovery branches with zero tests. Add Deno tests mocking esoApi covering both 409 branches and polling termination. *(testing T-7 + kieran-typescript)*

20. **Audit Detail render at 1000-target scale** — current test uses 1/1/1 fixture. Plan §693 calls for a 1000-target validation. Add bench/test producing a 100KB+ Detail JSON; verify audit-log viewer's `max-w-xs truncate` handles it. *(testing T-8)*

21. **TestForceSync_PatchForbiddenAtAPI doesn't assert audit detail.Reason** — checks `entry.Result==Denied` but never unmarshals Detail to assert `Reason == "rbac_denied"`. Add assertion to mirror `TestForceSync_RBACDenied`. *(testing T-10)*

### Documentation

22. **CLAUDE.md Build Progress section** — add a "Phase E (Write actions)" entry under Phase 14 documenting force-sync (30s in-flight window, MergePatch preserves operator annotations), bulk-refresh job model, three new audit actions, orphan-reaper, and any retention/UID-pin/X-Cluster-ID decisions made via the higher-priority todos. *(project-standards ps-001)*

23. **MergePatch carve-out in CLAUDE.md** — Architecture Principles says "Server-side apply for all YAML operations." Force-sync uses MergePatch on annotations. Add a one-line carve-out: "metadata-only annotation patches MAY use MergePatch." *(project-standards ps-002 info)*

24. **Single-replica deployment assumption** — `CompleteOrphans` at startup unconditionally reaps every IN-PROGRESS row. Single-replica only. Add a comment in `main.go` near the call site, and document in CLAUDE.md. *(reliability rel-8 + data-migrations + correctness 3x agreement)*

25. **URL design inconsistency** (`/refresh-namespace/{ns}` vs `/stores/{ns}/{name}/refresh-all`) — verb-as-noun vs resource/action. Locks in mixed conventions. **If renaming is acceptable**, switch to `/externalsecrets/namespaces/{ns}/{refresh-all,refresh-scope}`. Otherwise document the inconsistency in the API style guide. *(api-contract + maintainability 2x)*

## Proposed Approach

Land items 1-8 (small Go fixes) + 9-11 (small TS fixes) in a single "Phase E cleanup" PR. Track 12-14 separately if not subsumed by P1/P2 todos. Land 16-21 (test scenarios) in a sibling PR. Land 22-25 (docs) when the higher-priority work merges.

## Acceptance Criteria

Pick a subset; close the rest of this todo with a comment listing what was deferred. Each landed item should:

- [ ] Be a green-tests-clean change (no new failures, no skipped tests).
- [ ] Be small enough to review without a corresponding plan entry.
- [ ] Have its line in the changelog if it's a behavior change (1, 2, 7).
