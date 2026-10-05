---
title: Tracked apply — durable intent before mutation, and the lessons that are not visible in the code
date: 2026-10-05
last_updated: 2026-10-05
category: docs/solutions
module: backend/internal/changes
problem_type: convention
component: service_layer
severity: high
related_components: [database, api_layer, testing_framework]
tags: [tracked-apply, change-receipts, idempotency, durable-intent, server-side-apply, verification, rolling-update, operation-id, release-e]
applies_when:
  - "Changing backend/internal/changes (TrackedApply, VerifyOnce, ClassifyAPIError) or the receipt store"
  - "Changing the observed apply loop in backend/internal/yaml/applier.go or the tracked branch of HandleApply"
  - "Changing how the web client mints, stores or reuses a trackedOperationId"
  - "Adding another endpoint that must give a retrying client a truthful answer after a lost response"
  - "Changing ReconcileOrphans, the receipt sweep period, or the orphan grace"
---

# Tracked apply — durable intent before mutation

**Status:** active convention. Shipped as Release E, PRs #563 to #570. The plan is
`docs/plans/2026-09-10-release-e-tracked-changes-impl.md` (see its "As shipped"
section for deviations); this document keeps the reasoning that the code and tests
state only as rules.

## Context

`POST /yaml/apply` is a sequence of independent server-side-apply PATCHes. A client
whose response is lost (a dropped connection, the BFF's 30-second cap, a reload)
cannot tell whether the bundle was applied, and pressing Apply again can apply it a
second time. Opt-in tracking (`?trackedOperationId=<uuidv4>`) fixes this by making the
client-supplied id the primary key of a row in `change_receipts` that is written
**before** the cluster is touched. A retry under the same id then collides on the
primary key and is answered from the row instead of reaching the apply engine.

The receipt records references and outcomes only: a SHA-256 digest of the submitted
bytes, and per object the group/version/resource, namespace, name, UID, action and
error. It never stores manifest content.

## The protocol

`changes.Service.TrackedApply` (`backend/internal/changes/service.go`):

1. Validate (UUIDv4 id, authenticated user, at least one document). Digest the raw
   body. Detect Secret documents. Nothing is persisted yet, so a failure here is a 400.
2. `Insert` the row with state `applying`. A unique violation is the idempotency
   branch (below). `ErrReceiptInvalid` (input the store rejects before any SQL) is a
   400. Any other failure is a 503 and nothing was applied.
3. `MarkMutationStarted`. This stamps `mutation_started_at` and is what separates "the
   process died before touching the cluster" (reconciled to `failed`) from "it died
   during the apply" (reconciled to `unknown`).
4. Apply. The engine calls an observer after every attempted document, and the
   observer appends that document's outcome before the engine moves on.
5. `Finalize` with `applied`, `partial`, `failed` or, when recording broke, `unknown`.

A collision at step 2 reads the stored row and decides, in this order: a different
owner gets 409 `operation_id_conflict` with nothing disclosed; different content or
cluster gets 409 `operation_id_reused`; a row that is not terminal gets 409
`operation_in_flight` with the receipt id; a terminal row is replayed with
`tracking.replayed: true`. The apply engine is never called on any of these.

## Lessons that are not obvious from the code

### 1. The intent row has to exist before the first PATCH, and the writes that guard it have to be conditional

Writing the receipt after the apply cannot answer "did my apply happen" for the case
that matters, which is the process dying mid-apply. `MarkMutationStarted`,
`AppendObject`, `Finalize` and `ReconcileOrphans` are guarded
`UPDATE ... WHERE completed_at IS NULL` statements (`MarkMutationStarted` also requires
`mutation_started_at IS NULL`, and `ReconcileOrphans` also `state = 'applying'` and an
age bound). `SetVerification` has its own guard, on `verification_state` not already
being final. `SetOwnership` is unguarded: it stores the preview-time snapshot and
nothing reads it as a decision. For the guarded single-row writes, zero matching rows
makes the store distinguish `ErrReceiptNotFound` from `ErrReceiptAlreadyFinal`.

`ErrReceiptAlreadyFinal` is a control signal, not a failure to log and continue:

- from `MarkMutationStarted` it means reconciliation already closed the row as
  `failed` ("never started"), so the apply must **not** start. Stamping and applying
  anyway would make a receipt that says nothing was applied describe a cluster that
  was changed.
- from `AppendObject` it means the receipt was reaped under a live apply, so the
  engine must stop (the observer returns an error).
- from `Finalize` it is reported as a warning and the reported state becomes
  `unknown`.

### 2. The observer contract: append before the next document, strictly increasing, stop on error

`ApplyObserverFunc` is called exactly once per attempted document, in index order,
immediately after the document was applied or failed. If it returns an error the
engine must attempt no further document. The recorder enforces the contract: an
out-of-range, duplicate or out-of-order index is a recording failure, and a
disagreement between what the observer recorded and what the engine says it attempted
also fails recording. Every such path ends in `unknown`.

The ordering is the whole point. If the engine appended in a batch at the end, a crash
would lose every outcome. Appending one at a time means a crash leaves a truthful,
ordered prefix, and "anything after the prefix is unknown" is a statement the receipt
can actually support.

Post-mutation receipt writes (`AppendObject`, `Finalize`) run on a context detached
from request cancellation (`context.WithoutCancel`, 5-second bound). A client that
hangs up mid-bundle must not leave the receipt describing less than what happened.

### 3. A document that was never sent is `notAttempted`, not `indeterminate`

Two different things look alike from outside. A PATCH that was sent and then cut off
may have been committed (`indeterminate`). A document the engine never reached was
provably not sent, and there are two ways to not reach one:

- The request context ended. The observed apply loop checks the context before each
  document and again immediately before each PATCH, and an abandoned document is
  reported failed with `NotAttemptedError` and is **not** observed.
- Recording failed. The observer returned an error, so the engine stopped; the rest are
  reported failed with `NotAppliedError`, and the receipt finalizes `unknown` because
  the recorded prefix no longer matches what the cluster holds.

Both count in `tracking.notAttempted`. Reporting an unsent document as indeterminate
would make the verifier read an object that was never touched and give the receipt a
false trail.

All documents still appear in `results` as `failed`, because legacy clients (the
mobile wizard controller computes `allSucceeded` as `failed == 0`) would otherwise read
a truncated list as a complete success. `tracking.notAttempted` is how a new client
tells "not sent" from "rejected".

A replay of an `unknown` receipt is the opposite case: the lost process may have sent
documents after its last record, so the holes are counted in `tracking.unrecorded`,
with class `indeterminate`, and `notAttempted` stays 0.

### 4. Only definite rejections are classified; everything else is `indeterminate`

`ClassifyAPIError` is an allowlist. A Status with code 400, 401, 403, 404, 405, 409,
415, 422 or 429 means the API server rejected the request. Everything else (a
cancelled context, a timeout, a reset after the body was sent, a 500 wrapping an etcd
timeout, a 504, an error with no Status at all) is `indeterminate`, because the write
may have been committed before the error reached us.

503 is indeterminate on purpose. client-go turns any response without a Status body
into a generic 503, so a service mesh, load balancer or aggregator front door can
answer "upstream connect error" after the PATCH was already forwarded. A deny-list
that treated 5xx as "not applied" would verify nothing for exactly the failures that
need verifying. An indeterminate object is verified like a success; a definite failure
is skipped. Only the PATCH's error is classified: a failed pre-PATCH GET mutated
nothing.

For a Secret-bearing bundle the stored error text is replaced by its class plus a
fixed message, because admission messages can echo submitted values. The class is
always kept, since the verifier reads it.

### 5. The 503 is step-aware, and an insert failure must say `retrySameOperationId: true`

`receipt_store_unavailable` always carries `extra.applied = false`. Whether the client
should keep its operation id depends on where recording failed:

| Step | `retrySameOperationId` | Why |
|---|---|---|
| `insert` | true | `Insert` reports a collision only on a real unique violation. An unreachable or timed-out database therefore surfaces here even when this request is a **retry** of a send that already applied. A new id could then apply twice. Reusing the id is always safe: if the insert never committed the retry proceeds, and if it did the retry is told in-flight and then replays. |
| `read` | true | Reading the existing row failed while resolving a collision, so this is a retry and the original may have applied. |
| `mark` | false | The row was inserted by this very request and is finalized `failed`; a same-id retry would only replay that failure. Start a new attempt. |
| no store | false | Tracking is not configured. |

The first version of this told the client to mint a new id on an insert failure. That
was wrong in the one case that matters, and was corrected in a follow-up commit inside
#569.

### 6. Verdicts are persisted by the receipt owner only, and the first final verdict wins

`GET /changes/{id}/verification` writes (stateless polling, no background watcher, so
no user credential outlives its request). Two rules keep that write safe:

- **Owner only.** `VerifyOnce(..., VerifyOptions{Persist})` is persisted only when the
  caller is the owner. An admin or grantee gets a live evaluation under their own
  Kubernetes identity that is never stored. The k8sCenter admin role is app-level and
  every cluster call impersonates the caller, so admin does not imply `get`. If an
  admin's poll could persist, an admin who cannot read the workload would freeze the
  owner's receipt as `inconclusive`.
- **First final wins, atomically.** The guard is in the store's UPDATE, not in a
  read-then-write in Go, so two concurrent polls that both compute a final verdict
  cannot race: one write lands and the other gets `ErrReceiptAlreadyFinal` and reads
  the stored verdict back. This also stops a slow verifier overwriting the
  `inconclusive` that reconciliation recorded.

`VerificationResult.Persisted` is true only when this call's write landed a final
verdict, and the `change_verify` audit entry keys on it, so each persisted verdict is
audited once and a read-back or lost race is not.

Verification yields one check per recorded object that may exist on the cluster (a
success, or a failure classed `indeterminate`), plus one `inconclusive/outcome_unrecorded`
check per document index with no recorded outcome, so a receipt that lost outcomes can
never aggregate to `verified`. A document that definitely failed is skipped: it is not
on the cluster, so there is nothing to verify, and the receipt's execution state stays
`partial` or `failed` whatever the verification state says.

### 7. Reconcile is age-bounded because a rolling update runs two pods

The first design closed every non-terminal receipt at boot. During a rolling update the
new pod boots while the old pod is still finishing a live apply, so that reconcile would
mark the old pod's in-flight receipt `unknown` or `failed` and the next
`MarkMutationStarted` or `AppendObject` on the old pod would see
`ErrReceiptAlreadyFinal` and abort a healthy apply.

`ReconcileOrphans(ctx, olderThan)` therefore only touches rows with `state =
'applying'`, `completed_at IS NULL` and `created_at < now - olderThan`, and the
sweeper passes `store.ReceiptOrphanGrace` (10 minutes). A tracked apply is one live
request bounded by the 30-second proxy cap, so a row still open after ten minutes
belongs to a process that is gone. The cost of reaping late is a stale `applying` row
for a few minutes; the cost of reaping early is killing a live apply. Because a row too
young to reap at boot must still be reaped later, the sweep also runs every
`ReceiptOrphanGrace`, not only at startup. The remaining assumption is that no apply
runs longer than the grace.

A `previewed` row is never reaped (it was never going to apply). Retention removes it.

### 8. The client id lifecycle: reuse only on an unknown outcome, bound to the whole request key

The id is minted per user-initiated apply. It is reused only when the previous attempt's
outcome is unknown: no response, any 5xx (subject to the 503 table above), or 409
`operation_in_flight`. A definite answer (a 2xx, a 4xx, a 409 refusal) ends the attempt
and the next press mints a new id, because applying again is then a deliberate new
change.

The stored attempt is bound to the **full request key**: force, pin, repairOf, target
cluster, current user id and the YAML text. This matters in both directions. If the id
travelled with different content the server would refuse it as `operation_id_reused`;
if it travelled to a different user or cluster it would be a confusing conflict or, for
a same-owner collision, a wrong replay. The key is hashed (SHA-256) before it is used
as the `sessionStorage` key, and only the id, the first-send time and the digest are
stored, never the key, because the key contains the manifest and may contain Secret
values. Entries expire after 10 minutes (the reconcile grace) and are cleared on logout
and on success or a definite refusal.

### 9. What the receipt never holds

The digest is unsalted and covers raw bytes, so a trivially reformatted manifest is a
different change (`operation_id_reused`, by design). For a Secret-bearing receipt the
read path blanks `contentDigest` for everyone but the owner, since the digest of a
bundle whose Secret rows are filtered is an offline confirmation oracle for
low-entropy values. Reads return 404 for a receipt the caller may not see, so ids are not
enumerable.

## When to apply

- Before touching `TrackedApply`, the observer contract, or the observed apply loop:
  re-read sections 1 to 3. The tests that pin them are in
  `backend/internal/changes/service_test.go`, `backend/internal/yaml/tracked_apply_test.go`
  and the PostgreSQL-backed `tracked_apply_db_test.go`.
- Before adding any new status code to the classifier: it belongs on the allowlist only
  if it proves the server did not apply the request.
- Before changing the sweep period or grace: keep the period no longer than the grace.
- Before adding another endpoint that must answer a retry truthfully: the same shape
  applies (client-supplied key as primary key, row before side effect, guarded updates,
  replay from the row).

## See also

- `docs/plans/2026-09-10-release-e-tracked-changes-impl.md` ("As shipped (2026-10-05)")
- `docs/solutions/http-insecure-context-crypto.md` (why the client id and its storage key
  do not use `crypto.randomUUID` or `crypto.subtle`)
- `docs/solutions/postgres-test-harness-conventions.md` (how the DB behaviours here are
  tested, and the table-global sweep caveat)
- `docs/solutions/backend-resilience-conventions.md` (`recoverutil.Tick` for the sweep
  loop, and the fuzz targets added with this release)
