---
title: Idempotent create with a client request id, and why the client-side heuristic was abandoned
date: 2026-10-07
last_updated: 2026-10-07
category: docs/solutions
module: backend/internal/incidents
problem_type: convention
component: service_layer
severity: medium
related_components: [database, api_layer, frontend_stimulus]
tags: [idempotency, client-request-id, incidents, postgres, on-conflict, replay, release-d]
applies_when:
  - "Adding an endpoint whose response can be lost and whose retry must not create a duplicate"
  - "Changing IncidentStore.CreateWithRequestID or the create handler's replay branch"
  - "Writing a client that creates then captures (or otherwise chains) and may retry after a lost response"
  - "Comparing timestamps that round-trip through PostgreSQL"
---

# Idempotent create with a client request id

**Status:** active convention. Shipped as U25c, PR #588 (migration `000026`), during Release D. The plan is `docs/plans/2026-09-10-release-d-incidents-impl.md`.

## Context

`POST /incidents` creates a row. A client whose response is lost (a dropped connection, the proxy's 30-second cap, a remount, a token-refresh replay) cannot tell whether the incident exists, and pressing the button again creates a second one. The capture-to-incident flow (U25b, #587) makes this worse because it creates an incident and then captures into it, so a retry in the middle leaves a duplicate that the operator has to find and delete.

## Why the client-side heuristic was abandoned

The first version of #587 tried to answer "did my create already succeed?" on the client: list the owner's incidents and match on title and window. Review kept finding holes. A title is not unique, a window the user did not set is derived from the click time, a list page can miss the row, and two tabs by the same owner look identical. Every patch added another case. The information needed to answer the question exists only on the server at insert time, so the answer belongs there. A key minted once per intent by the client lets the database say "this is the same request" without any matching.

## The pattern

1. **A nullable column and a partial unique index per owner.** Migration `000026` adds `incidents.client_request_id uuid` (nullable) and `UNIQUE (owner_id, client_request_id) WHERE client_request_id IS NOT NULL`. Per owner means another user who sends the same id gets their own row and never sees this one. Nullable means every create without a key behaves exactly as before, and no backfill is needed.
2. **Insert, then read back.** `IncidentStore.CreateWithRequestID` runs `INSERT ... ON CONFLICT DO NOTHING`. A conflict waits for an in-flight insert to commit, so concurrent creates with one key make one row. The caller then reads back the owner's row for that key. The loop is bounded (three attempts) because the winner can be deleted between the conflict and the read; exhausting it returns `ErrIncidentBusy`, which the handler maps to 503 with `Retry-After`.
3. **200 replay versus 409 payload conflict.** A new row is 201. A key that matches an existing row with the same title, summary, window and cluster is a replay: 200 with the same body shape as the original. The same key with a different payload is 409 `client_request_id_conflict` and no incident body, because returning the stored incident to a request that asked for something else would hide the mistake. Deleting the incident frees the key.
4. **Tolerance compare for timestamps.** The window is compared at PostgreSQL's microsecond precision. Truncating the request's time and comparing for equality holds only when the stored value was floored; a text encoding rounds to the nearest microsecond and turns a genuine replay into a 409. Windows match when they are less than 1 microsecond apart, which holds for both encodings. A window the replay omits, or one the original lacked, is a mismatch.
5. **Audit only after the response is decided.** A new incident is audited as `incident_create`. A replay is audited as `incident_create_replayed`, inside `writeReplayedCreate`: success immediately before the 200, failure (with the incident id as detail) on every 409 and 503. A replay whose read-back fails is therefore never recorded as a success, and one incident is never audited as two creates.
6. **A stored owner that is not the caller is a 409.** It is logged at Warn. It cannot happen while the key is per owner; it is defence in depth if ownership ever moves.

## The client contract

Two callers implement it: the capture-to-incident button (`frontend/src/islands/CaptureToIncidentButton.tsx`, #587) and the New incident form in `frontend/src/islands/IncidentList.tsx`. Shared pieces are `frontend/lib/incident-create.ts` (the id generator and the pending record) and `frontend/src/components/incidents/errors.ts` (`createRefusedForGood`, `isCreateConflict`, `createConflictText`).

- **One id per intent.** `newClientRequestId()` returns a v4 UUID in hyphenated form (`crypto.randomUUID` when present, otherwise built from `crypto.getRandomValues`, because a homelab install may be served over plain HTTP). Anything else is 400 `invalid_client_request_id`, and nothing is stored or audited. `clientRequestId` is not echoed back in the response.
- **Resend while the outcome is unknown.** The capture button records the intent (id, title, summary, window start, `createdAt`) **before** sending, and every retry resends the stored id **and the stored payload byte-for-byte**, whatever the diagnosis now reports, because a rebuilt window (now minus one hour) differs from the first attempt's and turns a replay into a 409. Once the response gives an incident id, later attempts capture into that incident directly. The New incident form keeps its outstanding id (and an `uncertain` flag) in the list island so Cancel and reopen neither forget it nor mint a new one; a later submit resends the id with the **current** inputs. That is safe because the server answers a changed payload with `client_request_id_conflict` rather than creating a second incident.
- **When the key is dropped.** `createRefusedForGood` is true for 400, 413 and the no-database 503 (`incident_persistence_unavailable`). The capture button drops the key on those. The form drops it on no-database always, and on 400 or 413 only while no earlier attempt with that key had an unknown outcome; once one had, the key is kept, because that earlier attempt may have committed different inputs. A network error, a 5xx, `incident_busy`, 401, 403, 408, 429 and any other 4xx keep the key, because resending it cannot create a second incident.
- **Conflict.** `client_request_id_conflict` drops the key and shows "An incident for ... may already exist — check your incidents." with a link to the incident list. Nothing is created on the next click; only the explicit "Create a new incident anyway" mints a new id. The form keeps the conflict state across Cancel and reopen. The button records `{conflict: true}`, so the notice survives a remount. It also records it when the target is captured into an existing incident while a create's outcome is still unknown, instead of dropping the intent.
- **Pending records for the button.** They live in `sessionStorage` under `kubecenter.capture-pending:` (an in-memory map when storage is unavailable), keyed by the signed-in user's id and the target (cluster `""` and the local id normalised to one), so another identity on the same tab never reads them. `clearPendingCaptures` removes every identity's records on logout. An intent with no incident id yet is resent silently only for 15 minutes (`PENDING_INTENT_MAX_AGE_MS`) from its **last send** (`sentAt`, restamped on every send, falling back to `createdAt`); after that it reads like a conflict and the operator decides. An intent with a known incident id never goes stale.
- **Pending record for the form.** The form records its outstanding create (id, inputs including the window end, `createdAt`, `sentAt`) or its conflict under the same prefix, keyed `<user id>|new-incident-form` (`newIncidentFormKey`), before each send, so a reload or the conflict notice's link back to the list restores it the next time the form opens; the form does not open by itself. A restored create counts as outcome-unknown, so a later 400 or 413 keeps its id. A confirmed success, a refusal that drops the id, and logout clear it. Past the 15-minute bound from its last send the record is ignored and cleared rather than shown as a conflict: no create can still be in flight, and the incident list on the same page shows whether it committed. Writes after the response are made only while the stored record is still that create's, so an answer landing after logout does not restore it (#597).

## Pitfalls

- First write wins. A replay whose payload matches returns the stored incident unchanged; there is no update-on-replay.
- The key is only unique while the row exists. If the incident is deleted between the replay match and the read-back, the handler answers a retryable 503 `incident_busy`, and a retry with the same id then creates a new incident.
- The partial index is built non-concurrently in the migration. That is acceptable only because the column is new and the index matches no existing row. See the 000026 section of `backend/internal/store/migrations/NOTES.txt` for the lock analysis and rollback.

## Tests that carry the proof

`backend/internal/store/incident_create_idempotency_test.go` (DB-gated: replay, per-owner isolation, delete frees the key, 16 goroutines make one row, microsecond window) and the handler tests in `backend/internal/incidents/handler_test.go` (201 then 200, audit pairs, seven invalid spellings, the read-back 503). Removing `ON CONFLICT` or dropping `owner_id` from the index makes them fail.
