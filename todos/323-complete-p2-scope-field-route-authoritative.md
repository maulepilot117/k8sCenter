---
name: Make route authoritative for IssuerScope (ignore client-supplied scope)
status: complete
priority: p2
issue_id: 323
tags: [code-review, backend, architecture, pr-180]
dependencies: []
---

## Problem Statement

`backend/internal/wizard/issuer.go:73` has `Scope IssuerScope \`json:"scope"\``. The HandlePreview factory (`routes.go:294-297`) pre-seeds `Scope: IssuerScopeCluster` or `Namespaced` based on the route. But `json.Decode` overwrites the factory default with whatever the client sends. A client POSTing `scope: "cluster"` to `/wizards/issuer/preview` gets a ClusterIssuer rendered.

Not a security issue — `/yaml/apply` re-authorizes and Kubernetes RBAC rejects unauthorized cluster-scope creates. But architecturally the route should be authoritative.

The existing pattern in `rolebinding.go:15` uses `ClusterScope bool` without a `json` tag that the client controls — same asymmetry exists in the codebase, but this PR duplicates it.

## Findings

- Reviewers: architecture-strategist (P1), dhh-rails-reviewer (P2), pattern-recognition-specialist (P2).
- File: `backend/internal/wizard/issuer.go:73` (Scope field has `json:"scope"` tag).
- Decode site: `backend/internal/wizard/handler.go:40`.

## Proposed Solutions

### Option A — overwrite scope after decode in the handler factory
Change the factory closures in `routes.go:294-297` to return a typed function that wraps `HandlePreview` and re-applies `Scope` after `json.Decode`.
**Pros:** minimal type churn. **Cons:** adds indirection to the otherwise-clean factory pattern.

### Option B — mark Scope as unexported / `json:"-"` and pass it via context
Add `json:"-"` so decode ignores it; set the field on the returned input via a constructor that the route wires in.
**Pros:** compiler prevents clients from setting it; clean. **Cons:** `HandlePreview` factory signature needs a small tweak (already OK — factory already constructs the input).

### Option C — drop `IssuerScope` enum for `ClusterScope bool`
Align with `rolebinding.go`. One less enum to teach.
**Pros:** pattern consistency. **Cons:** this week's reviewers recommended the enum; DHH in particular flagged the duplication but preferred the bool.

## Recommended Action
<!-- filled at triage. Option B is likely cleanest. -->

## Technical Details
- `backend/internal/wizard/issuer.go:73` — remove `json:"scope"` tag or rename to `Scope IssuerScope \`json:"-"\``
- `backend/internal/server/routes.go:294-297` — ensure factory's set Scope survives decode
- Add unit test: POST `scope: "cluster"` to `/wizards/issuer/preview` — must render `kind: Issuer`.

## Acceptance Criteria
- [ ] Client-supplied `scope` is ignored at all three wizard endpoints.
- [ ] Unit test confirms this.

## Work Log
- 2026-04-14: Filed from PR #180 review.

## Resources
- PR #180
- Reviewers: architecture-strategist, dhh-rails-reviewer, pattern-recognition-specialist
