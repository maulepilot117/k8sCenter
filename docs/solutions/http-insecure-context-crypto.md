---
title: crypto.randomUUID and crypto.subtle do not exist on the HTTP-only homelab
date: 2026-10-05
last_updated: 2026-10-05
category: docs/solutions
module: frontend/lib
problem_type: convention
component: development_workflow
severity: medium
related_components: [helm_chart, testing_framework]
tags: [secure-context, crypto, randomuuid, subtle-crypto, uuid, sha256, http, homelab, math-random, operation-id]
applies_when:
  - "Writing browser code that needs a random id, a UUID, or a hash"
  - "Tempted to call crypto.randomUUID() or crypto.subtle.digest() in frontend code"
  - "Generating an id that is also a retry or idempotency key, or any other value that must be unpredictable"
  - "A feature works on localhost or behind HTTPS and silently does nothing on the homelab"
---

# `crypto.randomUUID` and `crypto.subtle` are undefined on the HTTP-only homelab

**Status:** active convention. Found while building tracked apply (Release E, #568), and
the same trap as the earlier dashboard-placement and gauge-id fixes.

## The problem

The homelab deployment is reached over plain HTTP, with no TLS in front of the
frontend. A browser exposes `crypto.randomUUID()` and all of `crypto.subtle`
only in a **secure context**: HTTPS, or `localhost`. Over HTTP to a hostname or LAN
address both are `undefined`. `crypto.getRandomValues()` is available in every context.

Developers hit this late, because every place they normally run the app is a secure
context: `localhost:5173` in dev, Playwright against localhost, and HTTPS deployments.
The failure on the homelab is a `TypeError: crypto.randomUUID is not a function` thrown
inside an event handler, which shows up as a button that does nothing. Dashboard widget
placement hit exactly this: the "Add widget" click handler threw and the feature looked
dead (`frontend/lib/dashboard/placement.ts`). Do not rely on a green e2e run to catch it.

## The rule

- **Never call `crypto.randomUUID()` or `crypto.subtle` unguarded in frontend code.**
- For a UUID, use `uuidv4()` from `frontend/lib/uuid.ts`. It uses `randomUUID` when the
  runtime has it, otherwise builds an RFC 4122 version 4 UUID from `getRandomValues`
  (version nibble 4, variant bits `10xx`), and **throws** when neither exists rather than
  degrading to a predictable id. It lowercases the result, because the server
  canonicalizes operation ids to lowercase; compare ids with `sameOperationId`, not `===`.
- For a hash, use a synchronous JavaScript SHA-256. `sha256Hex` in
  `frontend/lib/pending-apply.ts` is one; `crypto.subtle.digest` is async and also
  missing on HTTP. Use it for keys and fingerprints, not for anything that needs a
  vetted constant-time implementation.
- **Never use `Math.random` for an id that is a retry key, an idempotency key or a
  credential.** `Math.random` is acceptable only for cosmetic, non-security uniqueness
  where a collision is harmless (SVG gradient ids in `GaugeRing.tsx` and
  `SparklineChart.tsx`, and the widget instance suffix in `placement.ts`, which has its
  own redraw logic). A tracked-apply operation id is the primary key of a receipt and
  the thing that stops a retry from applying twice. The backend checks it with
  `uuid.Parse` plus `Version() == 4`, but a predictable id would still let one session
  collide with another's receipt. It must come from a CSPRNG, so it must come from
  `getRandomValues`.

## Testing it

`uuidv4` takes the crypto object as an argument (`uuidv4(c = globalThis.crypto)`), so the
unit tests (`frontend/lib/uuid_test.ts`) pass a fake with and without `randomUUID` and
assert the version and variant bits and the throw on a missing source. Do the same for any
new helper: inject the crypto source so the insecure-context branch is exercised in CI,
where the runtime is a secure context and would otherwise never take it.

## See also

- `docs/solutions/tracked-apply-durable-intent.md` (the operation-id lifecycle that needs
  both a UUID and a request-key hash)
- `frontend/lib/uuid.ts`, `frontend/lib/pending-apply.ts`
