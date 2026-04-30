---
title: "Cert-Manager configurable expiry thresholds (#7c)"
type: feat
status: complete
date: 2026-04-29
---

# Cert-Manager configurable expiry thresholds (#7c)

## Overview

Replace the two cert-manager package-level constants (`WarningThresholdDays = 30`, `CriticalThresholdDays = 7`) with per-Certificate and per-Issuer/ClusterIssuer overrides set via annotation. A Certificate's effective threshold resolves through a fixed chain: its own annotations win, then the annotations on the Issuer / ClusterIssuer it references, then the package defaults. The resolved values drive Status derivation, the `/certificates/expiring` filter, and the expiry-poller notification dispatcher uniformly — there's no path that bypasses the resolution.

## Problem Frame

Phase 11A landed cert-manager observatory with hardcoded 30/7-day thresholds. Operators have called this out in two scenarios that the global default can't serve:

1. **Short-lived certs** (e.g. ACME 90-day certs renewed at 30d) need a tighter warning window — 30d means they're already auto-renewed by the time we'd warn, so the warning is meaningless. A 14-day warning is more useful here.
2. **Long-lived internal CA certs** (1-year+ from a corporate CA) want a longer runway — 30 days is too late to coordinate a manual reissue.

Cert-manager itself doesn't expose such a setting; this is an observatory-side feature. Annotations on the cert-manager CRDs are the right surface because they're per-resource, kubectl-native, and don't require new k8sCenter config plumbing.

## Requirements Trace

- **R1.** A Certificate can carry annotations that override the warning + critical thresholds for that cert specifically.
- **R2.** An Issuer / ClusterIssuer can carry the same annotations; certs that reference it inherit those values when the cert itself doesn't override.
- **R3.** When neither cert nor issuer carries the annotation, the existing package-default constants apply (30 / 7) — preserving today's behavior for unannotated installations.
- **R4.** Resolved thresholds drive every consumer in the package: `Status` derivation, the `/certificates/expiring` endpoint filter, the `expiring` severity field, and the poller's notification-bucket assignment.
- **R5.** Resolved values are observable from the API and the UI: a Certificate detail response carries `warningThresholdDays`, `criticalThresholdDays`, and `thresholdSource` so operators can verify which annotation took effect.
- **R6.** Invalid annotation values (non-integer, negative, `crit >= warn`) are logged and silently fall through to the next layer; they never break the response or the poller.
- **R7.** No regression on the existing notification dedupe semantics (`(uid, threshold-bucket)`). A change to a cert's effective threshold that moves it across buckets emits one notification, just as a real time-decay crossing would.

## Scope Boundaries

- **No UI for editing annotations** in this scope. v1 sets values via `kubectl annotate` / manifest. The detail page surfaces the resolved values + source so operators can verify; an editor is a future enhancement.
- **No alert-rule integration.** Notifications continue to flow through the existing `NotificationService` + the same `certificate.expiring` / `certificate.expired` event kinds. The thresholds change WHEN those events fire, not WHERE they go.
- **No new dedupe key.** The poller continues to dedupe by `(uid, threshold-bucket)`. Bucket assignment now uses the resolved per-cert thresholds, so a threshold change that moves a cert across buckets correctly emits a fresh notification — no "stuck dedupe" behavior added.
- **No broader cert-manager config layer.** The thresholds are the only knob being made configurable here; other Phase 11A defaults (60s poll interval, 30s cache TTL) stay package-level.

## Context & Research

### Relevant Code and Patterns

- `backend/internal/certmanager/types.go:48-53` — `WarningThresholdDays = 30`, `CriticalThresholdDays = 7` constants. Three call sites consume them.
- `backend/internal/certmanager/normalize.go:11-36` — `computeStatus` reads `WarningThresholdDays` directly to derive `StatusExpiring`. Status derivation needs to move out of normalize (or accept the resolved threshold) so the same cert with different effective thresholds gets the right Status.
- `backend/internal/certmanager/handler.go:516-525` — `/certificates/expiring` filter consumes both constants directly. Needs per-cert lookup.
- `backend/internal/certmanager/poller.go:19-52` — `thresholdBucket(days int) threshold` consumes both constants directly. Needs to take the cert (or its resolved thresholds) so the bucket reflects the per-cert window.
- `backend/internal/certmanager/poller.go:97-134` — `Poller.check()` already keys dedupe by `cert.UID` and a single `threshold` bucket; bucket-as-int means a threshold change that moves a cert from "warning" to "none" correctly clears the dedupe entry. No structural change needed.
- `backend/internal/certmanager/normalize.go:40` — `normalizeCertificate(u *unstructured.Unstructured)` is the single entry point for cert annotations. Annotations live in `u.GetAnnotations()`.

### Institutional Learnings

- No `docs/solutions/` directory in this repo (confirmed during PR #205 review). Closest precedent for annotation-driven per-resource configuration in this codebase: nothing similar; this is a new pattern. Document it as a single-source-of-truth (one resolver function) so future per-resource toggles can follow the same shape.
- Phase D's review found that constants used in two places drift apart silently. Apply the same lesson here: define the annotation key strings as exported `const` so the frontend, tests, and resolver all reference the same string.

### External References

- cert-manager has no built-in expiry-threshold annotation; this is a k8sCenter-namespaced annotation (`kubecenter.io/...`). No external contract to align with.
- Skipped framework-docs research — the pattern is internal plumbing with no third-party integration surface.

## Key Technical Decisions

- **Annotation keys live on Certificates AND Issuers/ClusterIssuers under the same names.** Two keys total: `kubecenter.io/cert-warn-threshold-days`, `kubecenter.io/cert-critical-threshold-days`. The same string lookup runs against any of the three resource kinds. *Why*: operators won't have to memorize different key names per kind; mistyping `cert-` vs `certificate-` is a real footgun.
- **Resolution chain is fixed: cert > issuer > clusterissuer > default.** Cert-level wins outright, even if it sets only one of the two values (e.g. cert sets warn but not crit → warn comes from cert annotation, crit comes from the next layer down). *Why*: matches operator intuition — "if I set it on this cert, that's what I want for this cert"; falling back per-key avoids forcing operators to declare both values to override one.
- **`ThresholdSource` is an enum, not a free-form string.** Values: `"default"`, `"certificate"`, `"issuer"`, `"clusterissuer"`. *Why*: the frontend's "warns at 60d (from Issuer X)" tooltip needs to know which source emitted the value, not just "from somewhere". An enum keeps the wire contract typed.
- **Status derivation moves out of `normalizeCertificate`.** `normalizeCertificate` becomes purely "unstructured → typed", with `Status` computed by a separate `DeriveStatus(cert)` step that runs after threshold resolution. *Why*: normalize doesn't have the issuer context needed for inheritance; trying to thread it through `normalizeCertificate` would break the function's "stateless converter" shape.
- **Invalid annotations log + fall through, never error.** A cert with `kubecenter.io/cert-warn-threshold-days: "potato"` is treated as if the annotation were absent. *Why*: the alternative is breaking `/certificates` for that cert, which is worse than silently using defaults. The log line gives operators the diagnostic; the planned `ThresholdSource` field on the cert tells them what value actually applied.
- **Resolved values are stored back on the `Certificate` struct.** New fields `WarningThresholdDays int`, `CriticalThresholdDays int`, `ThresholdSource string`. *Why*: callers (handler `/expiring`, poller `check`) shouldn't have to know about issuer lookup — they read the resolved values directly. Single resolver, multiple consumers.
- **Poller calls the same handler-cached `CachedCertificates` path that already runs through `ApplyThresholds`.** No duplicate resolution work. *Why*: the poller's `fetchCertificates` already prefers the handler cache; piggy-backing on the cached resolved values keeps the threshold logic in one place.

## Open Questions

### Resolved During Planning

- **Q: Annotation key naming?** Resolved: `kubecenter.io/cert-warn-threshold-days` + `kubecenter.io/cert-critical-threshold-days`. Use the project's `kubecenter.io` namespace (already used for the issuer scope hint in the Phase 11B wizards — verify during implementation).
- **Q: Cert-level partial override behavior?** Resolved: each key resolves independently down the chain. Cert can set warn only and inherit crit from the issuer.
- **Q: Where does Status derivation live?** Resolved: extracted to a new `DeriveStatus(cert Certificate)` function called after threshold resolution.
- **Q: How does the poller see the resolved values?** Resolved: through `Handler.CachedCertificates`, which now returns thresholds-applied certs. The poller's existing fast-path already uses this; the fallback `dyn.List` path also runs `ApplyThresholds`.
- **Q: What about a Certificate that references an Issuer the user can't list?** Resolved: the resolver runs in the handler context with service-account visibility (the same context that already lists issuers for the cert-manager handler's other endpoints). Per-user RBAC happens after, on the response. No leak.

### Deferred to Implementation

- **Exact regex / parser for the annotation value.** `strconv.Atoi` plus a positive-integer guard is fine; the implementer will pick a small wrapper helper. Edge case: `strconv.Atoi("030")` parses to 30 — acceptable. Empty string after annotation key = treat as absent.
- **Whether to expose the unparsed-annotation log message in the API response** (e.g., `Certificate.thresholdValidationError string`). Probably not — `ThresholdSource` already makes it clear that a non-default source was attempted; the log is the diagnostic. Re-evaluate if a homelab smoke uncovers a real "I set the annotation but the UI shows default" case where the user can't easily check logs.
- **Sort order for `/certificates/expiring` when per-cert thresholds vary.** Today it's `daysRemaining` ascending. With per-cert thresholds, "10 days" might be a critical for one cert and "fine" for another — sorting by raw days remains correct (operators want the soonest-to-expire first regardless of threshold), but the severity column will visibly mix. Acceptable; document on the frontend.

## Implementation Units

- [ ] **Unit 1: Type changes + annotation reading in `normalize`**

**Goal:** Add the threshold fields and source enum to `Certificate` / `Issuer`. `normalizeCertificate` and `normalizeIssuer` read the cert-level / issuer-level annotations into pre-resolved fields (cert-level only — issuer inheritance is Unit 2). Status derivation moves out of `normalize`.

**Requirements:** R1, R6 (annotation parsing + invalid handling)

**Dependencies:** None.

**Files:**
- Modify: `backend/internal/certmanager/types.go` — declare annotation key constants, add fields to `Certificate` and `Issuer`, add `ThresholdSource` enum, retain `WarningThresholdDays` / `CriticalThresholdDays` package consts as the defaults
- Modify: `backend/internal/certmanager/normalize.go` — read cert annotations into the cert struct; extract `DeriveStatus(cert) Status` from `computeStatus`; `normalizeCertificate` no longer calls Status derivation directly
- Modify: `backend/internal/certmanager/normalize_test.go` — annotation parsing scenarios; cert without annotations preserves default behavior

**Approach:**
- New consts: `AnnotationWarnThreshold = "kubecenter.io/cert-warn-threshold-days"`, `AnnotationCriticalThreshold = "kubecenter.io/cert-critical-threshold-days"`.
- `Certificate` gains: `WarningThresholdDays int`, `CriticalThresholdDays int`, `ThresholdSource string` (zero values mean "not yet resolved" — Unit 3 fills them).
- `Issuer` gains: `WarningThresholdDays *int`, `CriticalThresholdDays *int` (pointer because Unit 2 needs to distinguish "not set" from "set to 0"). These are populated directly by `normalizeIssuer` from annotations.
- A small parser helper `parseThresholdAnnotation(val string) (int, bool)` in normalize.go: returns `(n, true)` only when value is a positive integer.
- `normalizeCertificate` now sets `cert.WarningThresholdDays = parsedFromAnnotation` if present, else leaves it zero (Unit 2's resolver takes over). Same for `cert.CriticalThresholdDays`.
- `normalizeCertificate` no longer sets Status to `StatusExpiring` based on `WarningThresholdDays`. Status derivation moves to `DeriveStatus` and is called by Unit 3's `ApplyThresholds`. The `StatusExpired`, `StatusFailed`, `StatusIssuing`, `StatusReady` paths stay in `normalize` since they don't depend on thresholds.
- Tests: cert with warn=60 annotation populates the field; cert with `cert-warn-threshold-days: "abc"` leaves the field zero; warn-only cert leaves crit at zero (resolver fills it later).

**Patterns to follow:**
- Existing `stringFrom(map, key)` helper pattern in normalize.go for safe map traversal.
- Existing constant naming (`StatusReady`, `StatusExpiring`) — use `Source*` prefix for the new enum.

**Test scenarios:**
- *Happy path*: cert with `kubecenter.io/cert-warn-threshold-days: "60"` → `cert.WarningThresholdDays == 60` after normalize.
- *Edge case*: cert with `cert-critical-threshold-days: "0"` (literal zero) → field stays zero (treat as "not set"; the parser rejects non-positive). Same outcome as no annotation.
- *Edge case*: cert with both annotations set → both fields populated.
- *Error path*: cert with `cert-warn-threshold-days: "potato"` → field stays zero, log line emitted (use `slog.Default()` in test or capture).
- *Error path*: cert with `cert-warn-threshold-days: "-5"` → field stays zero.
- *Edge case*: cert with no annotations map → fields stay zero, no panic.
- *Issuer happy path*: issuer with both annotations → `issuer.WarningThresholdDays != nil && *issuer.WarningThresholdDays == X`.
- *DeriveStatus happy path*: meshed cert with NotAfter 50 days out and `WarningThresholdDays = 60` → Status == `StatusExpiring` (post-Unit 3, but the unit-test for `DeriveStatus` lives here since that's where the function moves to).

**Verification:**
- `go test ./internal/certmanager/...` passes.
- Existing Status tests still pass (they pass `WarningThresholdDays = 30` implicitly via the package default; verify the new `DeriveStatus` accepts thresholds via the cert struct).

---

- [ ] **Unit 2: Threshold resolver + ApplyThresholds pipeline step**

**Goal:** Single function that walks the resolution chain (cert → issuer → clusterissuer → default) for one cert and returns the effective `(warn, crit, source)`. A higher-level `ApplyThresholds(certs, issuers, clusterIssuers, defaults)` mutator runs the resolver over a slice of certs and computes Status. This is the seam that handler + poller both call.

**Requirements:** R2, R3, R4, R5

**Dependencies:** Unit 1 (annotation fields populated).

**Files:**
- Create: `backend/internal/certmanager/thresholds.go` — `ResolveCertThresholds(cert, issuersByName, clusterIssuersByName, logger) (warn, crit int, source string)` and `ApplyThresholds(certs []Certificate, issuers []Issuer, clusterIssuers []Issuer, logger) []Certificate`.
- Create: `backend/internal/certmanager/thresholds_test.go` — table-driven tests across the resolution matrix.

**Approach:**
- Resolver chain per key (warn and crit are independent):
  1. If cert annotation parsed to a positive int → use it; source for that key is `"certificate"`.
  2. Else if cert.IssuerRef points at an Issuer that has the annotation set → use it; source for that key is `"issuer"`.
  3. Else if `IssuerRef.Kind == "ClusterIssuer"` and the ClusterIssuer has the annotation → source `"clusterissuer"`.
  4. Else → use the package defaults (`WarningThresholdDays`, `CriticalThresholdDays`); source `"default"`.
- A single `ThresholdSource` field on the cert. When the two keys resolve to different sources (e.g., warn from cert, crit from issuer), the source string aggregates as the strongest source — `"certificate"` wins over `"issuer"` wins over `"clusterissuer"` wins over `"default"`. (Frontend tooltip can elaborate.)
- After resolution, sanity-check `crit < warn`. If violated, log and fall through to defaults for both keys, source = `"default"`.
- `ApplyThresholds` builds two name → Issuer maps once, then loops the cert slice running `ResolveCertThresholds` and calling `DeriveStatus` to populate `cert.Status`.

**Patterns to follow:**
- Phase D's `goldenSignalQueryNames` pattern: define one ordered list / canonical mapping, derive maps from it. Here: define annotation keys as consts, single resolver function, multiple call sites.
- `backend/internal/servicemesh/handler.go:filterByRBAC` — clean generic pattern for "loop over slice, apply per-item check, return filtered/transformed slice." Less generic here (no T parameter needed) but the shape is the same.

**Test scenarios:**
- *Happy: cert annotation only* — cert sets warn=60, no issuer annotations → resolved (60, default-7, "certificate").
- *Happy: issuer annotation only* — cert no annotation, issuer sets warn=60+crit=14 → resolved (60, 14, "issuer").
- *Happy: ClusterIssuer fallback* — cert references a ClusterIssuer with annotations → resolved (..., ..., "clusterissuer").
- *Happy: full default* — no annotations anywhere → (30, 7, "default").
- *Mixed: cert sets warn, issuer sets crit* — resolved (cert-warn, issuer-crit, "certificate") — strongest source wins for the aggregate field.
- *Edge: cert references missing Issuer* — issuer-by-name lookup returns nothing → fall through to defaults; no panic.
- *Edge: warn ≤ crit* — resolved values violate the constraint → log + fall to defaults; source = `"default"`.
- *Edge: empty cert/issuer slice* — `ApplyThresholds` is a no-op and returns the input unchanged.
- *Status integration*: cert with NotAfter in 50 days + resolved warn=60 → `cert.Status == StatusExpiring` (because `DeriveStatus` is called inside `ApplyThresholds`).
- *Status integration*: cert with NotAfter in 5 days + resolved warn=14, crit=3 → `cert.Status == StatusExpiring` (still in warning band; not below crit). Confirms that `DeriveStatus` only flips to Expiring at warn boundary, not crit (severity is the bucket's job, in Unit 4).

**Verification:**
- `go test ./internal/certmanager/...` passes; new file is the bulk of the new coverage.

---

- [ ] **Unit 3: Handler integration — read paths + `/expiring` filter**

**Goal:** Plumb `ApplyThresholds` into every handler read path that currently consumes the package-level constants. `CachedCertificates` returns thresholds-resolved certs so the poller and the HTTP handlers both see the same view.

**Requirements:** R4 (Status + /expiring use resolved thresholds)

**Dependencies:** Unit 2 (resolver exists).

**Files:**
- Modify: `backend/internal/certmanager/handler.go` — call `ApplyThresholds` inside the cert-list read path (one place: the function that builds the response from cached unstructured items). Update `/expiring` filter to consume `cert.WarningThresholdDays` / `cert.CriticalThresholdDays` instead of the package consts.
- Modify (if applicable): the path that backs `CachedCertificates` — same hook point.
- Modify: `backend/internal/certmanager/handler_test.go` (if it exists; create a focused test if not) — assert `/expiring` returns a cert that has `daysRemaining=20` AND a per-cert `warn=14` annotation (i.e., shouldn't be in the response, even though the global default would have included it).

**Approach:**
- Add a `thresholds(ctx)` helper on `Handler` that returns the cached issuer + clusterissuer slices the cert-manager handler already lists. Centralizing avoids re-fetching per request.
- The cert-list build site does:
  ```
  certs := normalize(unstructured items)
  issuers, clusterIssuers := h.thresholds(ctx)
  certs = certmanager.ApplyThresholds(certs, issuers, clusterIssuers, h.Logger)
  ```
- `/certificates/expiring` filter changes from `if *c.DaysRemaining > WarningThresholdDays { continue }` to `if *c.DaysRemaining > c.WarningThresholdDays { continue }`. Same for the critical-severity flip.
- `/certificates/{ns}/{name}` detail endpoint applies the same step so the detail response carries resolved thresholds + source.

**Patterns to follow:**
- Existing handler cache pattern at `handler.go` (Phase 11A) — minimize per-request fetches.

**Test scenarios:**
- *Happy: /expiring respects per-cert override* — cert with 20 days remaining, `cert-warn-threshold-days: "14"` → not in /expiring response. Without the override, would be included.
- *Happy: /expiring respects issuer inheritance* — cert with no annotation, issuer with `cert-critical-threshold-days: "30"` and the cert has 25 days → response severity is `"critical"`, not `"warning"`.
- *Detail endpoint*: response includes `warningThresholdDays: 60`, `criticalThresholdDays: 14`, `thresholdSource: "issuer"`.
- *Regression*: existing tests that don't set annotations still pass — global defaults still apply.

**Verification:**
- `go test ./internal/certmanager/...` passes.
- Smoke: `curl /api/v1/certificates/expiring` on a homelab annotated cert and confirm the response respects the override.

---

- [ ] **Unit 4: Poller integration**

**Goal:** Poller's `thresholdBucket` consumes the per-cert thresholds. `Poller.tick` already pulls from `CachedCertificates` (which now returns resolved certs after Unit 3) — the bucket function takes the cert as input rather than reading package constants.

**Requirements:** R4 (notifications fire at the resolved thresholds), R7 (dedupe semantics preserved)

**Dependencies:** Unit 3 (cached certs are pre-resolved).

**Files:**
- Modify: `backend/internal/certmanager/poller.go` — change `thresholdBucket(days int)` to `thresholdBucket(cert Certificate)` (or a small struct). `Poller.check` passes the whole cert; the bucket function reads `cert.WarningThresholdDays` / `cert.CriticalThresholdDays` (assumed populated by Unit 3 or Unit 2's fallback path).
- Modify: `backend/internal/certmanager/poller.go` — `fetchCertificates` fallback path (when handler is nil) now also runs `ApplyThresholds` so the `dyn.List` direct path doesn't bypass resolution.
- Modify: `backend/internal/certmanager/poller_test.go` — table-driven test for the bucket function with mixed per-cert thresholds.

**Approach:**
- `thresholdBucket(c Certificate) threshold`:
  ```
  warn := c.WarningThresholdDays
  crit := c.CriticalThresholdDays
  if warn == 0 { warn = WarningThresholdDays }   // belt-and-suspenders default
  if crit == 0 { crit = CriticalThresholdDays }
  switch {
    case days < 0: return thresholdExpired
    case days <= crit: return thresholdCritical
    case days <= warn: return thresholdWarning
    default: return thresholdNone
  }
  ```
- The fallback defaults inside the bucket function are a defense-in-depth measure: if a future caller forgets to run `ApplyThresholds`, the poller still emits sensible (global-default) notifications instead of treating warn=0 as "no warning ever".
- Dedupe key (`p.dedupe[c.UID]`) stays the same. A threshold change that moves a cert across buckets correctly emits a fresh notification because the bucket comparison is by enum value, not threshold value.

**Patterns to follow:**
- Existing `Poller.check` flow at `poller.go:97-134`. Only `thresholdBucket` signature changes; the dedupe/emit shape is preserved.

**Test scenarios:**
- *Happy*: cert with `WarningThresholdDays = 60`, `DaysRemaining = 50` → bucket = `thresholdWarning`.
- *Happy*: cert with `WarningThresholdDays = 14`, `DaysRemaining = 20` → bucket = `thresholdNone` (not yet in warning).
- *Edge*: cert with zero thresholds (someone bypassed `ApplyThresholds`) + `DaysRemaining = 25` → falls through to package default (`30`/`7`) → `thresholdWarning` (preserves pre-7c behavior).
- *Dedupe regression*: `Poller.check` called twice with the same cert + same bucket → second call returns no records.
- *Notification fires at the new threshold*: cert with `cert-warn-threshold-days: "14"`, days = 13 → notification emitted with severity `"warning"`. Same cert at days = 31 (above default 30, below user-set warn) → no notification (shouldn't have been emitted under either contract).

**Verification:**
- `go test ./internal/certmanager/...` passes.
- Smoke: annotate a homelab cert with a tight warn threshold; observe that the Notification Center receives the event when the cert crosses that line.

---

- [ ] **Unit 5: Frontend display + TS types + docs flip**

**Goal:** Surface resolved thresholds + source in the Certificate detail island. Update TS mirror types. CLAUDE.md API line + roadmap tick.

**Requirements:** R5 (operators can see which annotation took effect)

**Dependencies:** Units 1–4 (response shape final).

**Files:**
- Modify: `frontend/lib/certmanager-types.ts` (or wherever the `Certificate` interface lives; locate via current repo structure) — add `warningThresholdDays`, `criticalThresholdDays`, `thresholdSource: "default" | "certificate" | "issuer" | "clusterissuer"`.
- Modify: `frontend/islands/CertificateDetail.tsx` — small "Threshold" row in the existing summary panel. Format: `"Warns at: 60d, critical at 14d (Issuer letsencrypt-prod)"`. Use a tooltip on the source label for a longer explanation.
- Modify: `CLAUDE.md` — roadmap item #7c marked `[x]`; brief Phase note appended to Build Progress (or merged into the existing Phase 11A entry as a follow-up).
- Modify: `plans/cert-manager-configurable-expiry-thresholds.md` — flip `status: complete` after merge.

**Approach:**
- Keep the UI strictly informational. No edit affordance — users `kubectl annotate` directly; the UI confirms what took effect.
- Frontend shows the resolved values in plain English; the source enum maps to a label (`"Default"`, `"This certificate"`, `"Issuer <name>"`, `"ClusterIssuer <name>"`).
- No new island; the existing `CertificateDetail.tsx` already renders the cert's metadata table — add one row.
- TS types stay in the same module that mirrors the Go types; don't break existing imports.

**Patterns to follow:**
- Phase D3's `MeshGoldenSignals` pattern for an additive informational tile: silently absent when data unavailable, themed when present.
- Existing CertificateDetail row pattern (label + value).

**Test scenarios:**
- Test expectation: none required for the UI tile (matches Phase D3 / project frontend convention — no component-test culture in this repo). `deno fmt --check`, `deno lint`, `deno check` clean.
- For the docs unit: `make helm-lint` + `make helm-template` aren't impacted by this change (no chart edits).

**Verification:**
- Frontend type-check + lint clean.
- Manual smoke on homelab: annotate a Certificate with a custom threshold, refresh the detail page, confirm the row reads correctly.
- Roadmap item #7c flipped to `[x]` after the PR merges.

## System-Wide Impact

- **Interaction graph:** New dependency from `certmanager.Handler` and `certmanager.Poller` to a single `ApplyThresholds` step. No middleware changes. No new HTTP routes — existing endpoints gain three response fields per cert (`warningThresholdDays`, `criticalThresholdDays`, `thresholdSource`).
- **Error propagation:** Annotation parse failures log + degrade to defaults. No 5xx introduced. `crit >= warn` violations same.
- **State lifecycle risks:** Poller dedupe key (`(uid, threshold-bucket)`) is unchanged in shape. A threshold annotation change that moves a cert across buckets emits one fresh notification — same behavior as a real time-decay crossing. No "stuck dedupe" scenario.
- **API surface parity:** Backward-compatible. Pre-7c clients ignoring the new fields see the same response (Status enum, daysRemaining). Frontend adds a small row; clients consuming the JSON directly are unaffected.
- **Integration coverage:**
  (a) Cert with annotation + matching issuer with annotation → cert wins.
  (b) Cert without annotation + ClusterIssuer with annotation → ClusterIssuer wins.
  (c) Cert with invalid annotation → falls through to issuer / default.
  (d) Cert references a missing Issuer → falls through to default; no panic.
  (e) Threshold change at runtime (operator updates annotation) → next poll cycle picks up the new threshold; dedupe map clears stale entries.
- **Unchanged invariants:**
  - The `WarningThresholdDays = 30` / `CriticalThresholdDays = 7` package consts stay with their pre-7c values; they're now defaults, not the only path.
  - Existing notifications continue using `notifications.SourceCertManager` and `certificate.expiring` / `certificate.expired` event kinds.
  - Existing API endpoints + URL shapes — only response bodies are extended, additively.
  - `normalizeCertificate` keeps converting unstructured → typed; only Status derivation moves.
  - Helm chart RBAC unchanged — no new resource kinds.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Status derivation moving out of `normalize` breaks callers that expect `cert.Status` to be set immediately after `normalizeCertificate` | `ApplyThresholds` runs synchronously in every read path; document in `normalizeCertificate` godoc that Status is empty until `ApplyThresholds` runs. Tests cover the integration. |
| Operators set conflicting annotations on cert + issuer; surprise about which wins | The resolver order is documented in CLAUDE.md; the `ThresholdSource` field on every Certificate response shows operators exactly which layer applied. |
| Annotation value drift (e.g. "30 days" with a unit suffix) causes silent fallback to default | Parser is strict (positive integer only). Log line includes the cert/namespace name so operators can find it. Future: surface validation errors via a future `Certificate.thresholdValidationError` field if smoke testing finds operators don't read logs. |
| Notification flapping if an operator toggles an annotation rapidly | Dedupe key is `(uid, bucket)` — bucket changes still emit notifications, which is correct behavior for a deliberate annotation change. Mitigated by operator discipline; flag if real-world toggling becomes an issue. |
| Resolved thresholds cached too aggressively, so an annotation update doesn't take effect for 30s | The cert-manager handler cache TTL is 30s (Phase 11A). One-cycle latency is acceptable; document in the operator-facing docs. |
| Cross-cluster: resolution must happen per-cluster, not against the local cluster's issuers | Existing handler is local-cluster-only for the cache; remote clusters use direct dynamic-client paths. `ApplyThresholds` is local to the cert+issuer slices passed in, so it works correctly for either path as long as the slices come from the same cluster. Verify during implementation that the remote-cluster path doesn't accidentally cross-pollinate. |

## Documentation / Operational Notes

- Add a paragraph to operator docs (CLAUDE.md or a future operator guide): "Per-Certificate / per-Issuer / per-ClusterIssuer thresholds via `kubecenter.io/cert-warn-threshold-days` and `kubecenter.io/cert-critical-threshold-days`. Resolution chain: cert > issuer > clusterissuer > default. Values must be positive integers; invalid annotations fall back silently. Up to one polling cycle (30s) for an annotation change to take effect."
- Roadmap item #7c marked `[x]` post-merge.
- No alert routing changes — operators using the existing `SourceCertManager` notification rules continue to receive events at the new thresholds without rule edits.

## Sources & References

- Phase 11A plan: `plans/` (cert-manager observatory) and CLAUDE.md Build Progress entry.
- Annotation surface: `backend/internal/certmanager/types.go` constants; `backend/internal/certmanager/normalize.go` `computeStatus`; `backend/internal/certmanager/poller.go` `thresholdBucket`; `backend/internal/certmanager/handler.go` `/expiring` handler.
- Roadmap entry: `CLAUDE.md` "Future Features (Roadmap)" item 7c.
